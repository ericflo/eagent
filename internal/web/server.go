// Package web serves eagent's browser UI: a live view of any session in the
// project (running in this process or in a terminal elsewhere), a session
// browser, task and tool-call drill-down, and configuration.
//
// The log on disk is the source of truth, so the UI tails the JSONL files
// rather than depending on the process that writes them. Messages to a
// running session go through its inbox directory (see harness.PostInbox).
// Finished sessions are resumed in this process when the user writes to them.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/projection"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/settings"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
	webstatic "github.com/ericflo/eagent/internal/web/static"
)

// Server is one project's UI.
type Server struct {
	Project string
	Preset  string // default preset for sessions started from the UI
	Bundle  string // default bundle for sessions started from the UI
	Verbose bool

	mu      sync.Mutex
	running map[string]*hosted
	mux     *http.ServeMux
	logf    func(string, ...any)

	token    string // per-process page token echoed on every mutating request
	addr     string // the address the server listens on, for Host checks
	checksMu sync.Mutex
}

type hosted struct {
	rt     *harness.Runtime
	ui     *webUI
	cancel context.CancelFunc
	done   chan struct{}
	code   int
}

// New builds a server for a project directory.
func New(project string, logf func(string, ...any)) *Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{Project: project, running: map[string]*hosted{}, token: newToken(), logf: logf}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	files := http.FileServer(http.FS(webstatic.Files))
	indexRaw, _ := webstatic.Files.ReadFile("index.html")
	index := []byte(strings.Replace(string(indexRaw), "__EAGENT_TOKEN__", s.token, 1))
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Any path without an extension is the single-page app; FileServer
		// would redirect /index.html to /, so serve it directly.
		if r.URL.Path == "/" || !strings.Contains(strings.TrimPrefix(r.URL.Path, "/"), ".") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(index)
			return
		}
		files.ServeHTTP(w, r)
	})
	s.mux.HandleFunc("GET /api/sessions", s.listSessions)
	s.mux.HandleFunc("POST /api/sessions", s.guard(s.startSession))
	s.mux.HandleFunc("GET /api/sessions/{id}", s.getSession)
	s.mux.HandleFunc("GET /api/sessions/{id}/events", s.getEvents)
	s.mux.HandleFunc("GET /api/sessions/{id}/stream", s.stream)
	s.mux.HandleFunc("GET /api/sessions/{id}/attachments/{name}", s.getAttachment)
	s.mux.HandleFunc("POST /api/sessions/{id}/message", s.guard(s.postMessage))
	s.mux.HandleFunc("POST /api/sessions/{id}/answer", s.guard(s.postAnswer))
	s.mux.HandleFunc("POST /api/sessions/{id}/stop", s.guard(s.postStop))
	s.mux.HandleFunc("POST /api/sessions/{id}/resume", s.guard(s.postResume))
	s.mux.HandleFunc("GET /api/config", s.getConfig)
	s.mux.HandleFunc("PUT /api/config", s.guard(s.putConfig))
	s.mux.HandleFunc("PUT /api/config/raw", s.guard(s.putConfigRaw))
	s.mux.HandleFunc("POST /api/config/test", s.guard(s.testRoute))
	s.mux.HandleFunc("GET /api/config/presets/{name}", s.getPresetConfig)
	s.mux.HandleFunc("GET /api/catalog", s.getCatalog)
	s.mux.HandleFunc("POST /api/config/bundles", s.guard(s.saveBundle))
	s.mux.HandleFunc("GET /api/prompts/{name}", s.getPrompt)
	s.mux.HandleFunc("PUT /api/prompts/{name}", s.guard(s.putPrompt))
	s.mux.HandleFunc("DELETE /api/prompts/{name}", s.guard(s.deletePrompt))
	s.mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "project": s.Project})
	})
}

// ---- helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}

func (s *Server) root() string { return store.Root(s.Project) }

func (s *Server) resolve(ref string) (store.Info, error) {
	return store.Resolve(s.root(), ref)
}

// alive reports whether some process holds the session lock.
func alive(sessionPath string) bool { return store.Alive(sessionPath) }

// Hosted reports whether this process is running the session.
func (s *Server) Hosted(id string) bool { return s.isHosted(id) }

// Resume reopens a session nobody is running with a reply the user sent to
// its thread from the phone, the way postMessage resumes one from the
// browser. The reply is the session's next turn (or the answer to the
// question it stopped on); its source keeps the mirror from echoing it.
func (s *Server) Resume(sessionID string, msg integration.PhoneMessage) error {
	info, err := store.Resolve(store.Root(s.Project), sessionID)
	if err != nil {
		return err
	}
	if info.ID != sessionID {
		return fmt.Errorf("session %s not found", sessionID)
	}
	if s.isHosted(info.ID) || alive(info.Path) {
		return errors.New("the session is running; its own mirror reads the thread")
	}
	return s.host(info.Path, harness.Options{Project: s.Project, Interactive: true, Verbose: s.Verbose, Prompt: msg.Text, PromptSource: "finalechat", PromptAttachments: msg.Attachments}, "", "", true)
}

// ---- sessions ---------------------------------------------------------------

// Session views are shared with the portable artifact exporter.
type SessionSummary = projection.SessionSummary
type SessionDetail = projection.SessionDetail
type TaskView = projection.TaskView
type ProcView = projection.ProcView
type ScheduleView = projection.ScheduleView
type UsageView = projection.UsageView

var priceFor = projection.PriceFor
var estimateCost = projection.EstimateCost

func projectionInfo(info store.Info) projection.Metadata {
	return projection.Metadata{ID: info.ID, Started: info.Started, Modified: info.Modified, Subsessions: info.Subsessions, Size: info.Size}
}

func (s *Server) summarize(info store.Info) (SessionSummary, *state.State, error) {
	evs, err := store.Read(info.Path)
	if err != nil {
		return SessionSummary{}, nil, err
	}
	st := state.Replay(evs)
	if set, err := prompts.Load(s.Project); err == nil {
		st.SetPrompts(set)
	}
	return s.summarizeState(info, st), st, nil
}

// summarizeState summarizes an already-folded log.
func (s *Server) summarizeState(info store.Info, st *state.State) SessionSummary {
	sum := projection.Summary(projectionInfo(info), st)
	s.mu.Lock()
	_, sum.Hosted = s.running[info.ID]
	s.mu.Unlock()
	sum.Alive = sum.Hosted || alive(info.Path)
	switch {
	case sum.Alive && st.Idle() && st.Question != nil:
		sum.Status = "awaiting-input"
	case sum.Alive && st.Idle():
		sum.Status = "idle"
	case sum.Alive:
		sum.Status = "running"
	case !st.Ended:
		sum.Status = "interrupted"
	}
	return sum
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	infos, err := store.List(s.root())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	out := make([]SessionSummary, 0, len(infos))
	for i := len(infos) - 1; i >= 0; i-- { // newest first
		sum, _, err := s.summarize(infos[i])
		if err != nil {
			continue
		}
		out = append(out, sum)
	}
	writeJSON(w, out)
}

func (s *Server) detail(info store.Info) (*SessionDetail, error) {
	_, st, err := s.summarize(info)
	if err != nil {
		return nil, err
	}
	return s.detailState(info, st), nil
}

// detailState renders the detail of an already-folded log.
func (s *Server) detailState(info store.Info, st *state.State) *SessionDetail {
	d := projection.Detail(projectionInfo(info), st)
	sum := s.summarizeState(info, st)
	d.SessionSummary = sum
	s.mu.Lock()
	if h := s.running[info.ID]; h != nil {
		live := h.ui.status()
		d.Live = &live
	}
	s.mu.Unlock()
	if d.Live == nil && sum.Alive {
		// Not hosted here: derive activity from open turns in the log.
		live := harness.Status{OrchestratorBusy: st.Turns[event.ActorOrchestrator] > 0, NarratorBusy: st.Turns[event.ActorNarrator] > 0, ContextTokens: d.Context}
		if live.OrchestratorBusy {
			live.OrchestratorFor = time.Since(st.TurnSince[event.ActorOrchestrator]).Round(time.Second).String()
		}
		for _, t := range st.RunningTasks() {
			if t.Status == "running" {
				live.TasksRunning++
			} else {
				live.TasksQueued++
			}
		}
		live.Procs = len(st.RunningProcs())
		d.Live = &live
	}
	return d
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	info, err := s.resolve(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	d, err := s.detail(info)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, d)
}

// EventView is an event plus its source location.
type EventView struct {
	event.Event
	File string `json:"file"`
	Line int    `json:"line"`
}

func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	info, err := s.resolve(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	task := r.URL.Query().Get("task")
	out := make([]EventView, 0, 256)
	for _, ev := range evs {
		if ev.Seq <= after {
			continue
		}
		if task != "" && ev.Task != task {
			continue
		}
		out = append(out, EventView{Event: ev, File: ev.Source.File, Line: ev.Source.Line})
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, out)
}

// stream tails the session: every new event as an SSE "append" and a status
// snapshot every couple of seconds.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	info, err := s.resolve(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, errors.New("streaming unsupported"))
		return
	}
	// One reducer per stream, advanced with the bytes the tailer reads, so
	// an idle tab does not re-read and re-fold the whole log for every
	// status frame.
	evs, err := store.Read(info.Path)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	st := state.Replay(evs)
	if set, err := prompts.Load(s.Project); err == nil {
		st.SetPrompts(set)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	send := func(kind string, v any) {
		raw, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw)
		flusher.Flush()
	}
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	statusTick := time.NewTicker(2 * time.Second)
	defer statusTick.Stop()
	emitStatus := func() { send("status", s.detailState(info, st)) }
	emitStatus()
	tail := newTailer(info.Path, after)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			for _, ev := range tail.next() {
				// ?after=N may re-deliver events the replay already folded;
				// applying them twice would double token totals.
				if ev.Seq > st.LastSeq() {
					st.Apply(ev)
				}
				send("append", EventView{Event: ev, File: ev.Source.File, Line: ev.Source.Line})
			}
		case <-statusTick.C:
			emitStatus()
		}
	}
}

// tailer reads only the bytes appended since its last call, across all
// subsession files, so following a large session stays cheap.
type tailer struct {
	dir     string
	after   int64
	offsets map[string]int64 // file -> bytes consumed
	lines   map[string]int   // file -> lines consumed
}

func newTailer(dir string, after int64) *tailer {
	return &tailer{dir: dir, after: after, offsets: map[string]int64{}, lines: map[string]int{}}
}

func (t *tailer) next() []event.Event {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []event.Event
	for _, name := range names {
		path := filepath.Join(t.dir, name)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		off := t.offsets[name]
		if _, err := f.Seek(off, 0); err != nil {
			f.Close()
			continue
		}
		data, err := readAvailable(f)
		f.Close()
		if err != nil || len(data) == 0 {
			continue
		}
		// Only complete lines; the remainder is re-read next time.
		cut := strings.LastIndexByte(string(data), '\n') + 1
		if cut == 0 {
			continue
		}
		for _, line := range strings.Split(string(data[:cut]), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			t.lines[name]++
			var ev event.Event
			if json.Unmarshal([]byte(line), &ev) != nil {
				continue
			}
			ev.Source = event.Source{File: name, Line: t.lines[name]}
			if ev.Seq > t.after {
				out = append(out, ev)
				t.after = ev.Seq
			}
		}
		t.offsets[name] = off + int64(cut)
	}
	return out
}

func readAvailable(f *os.File) ([]byte, error) {
	var buf []byte
	chunk := make([]byte, 64<<10)
	for {
		n, err := f.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf, nil
			}
			return buf, err
		}
		if len(buf) > 16<<20 {
			return buf, nil
		}
	}
}

// ---- interaction ------------------------------------------------------------

type messageBody struct {
	Text       string `json:"text"`
	QuestionID string `json:"question_id"`
	Prompt     string `json:"prompt"`
	Config     string `json:"config"`
	Preset     string `json:"preset"`
	UseProject bool   `json:"use_project"`
}

func readBody(r *http.Request) (messageBody, error) {
	var b messageBody
	err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&b)
	return b, err
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	info, err := s.resolve(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	b, err := readBody(r)
	if err != nil || strings.TrimSpace(b.Text) == "" {
		writeErr(w, 400, errors.New("text is required"))
		return
	}
	if alive(info.Path) || s.isHosted(info.ID) {
		if err := harness.PostInbox(info.Path, harness.InboxMessage{Type: "message", Text: b.Text, From: "web"}); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, map[string]any{"delivered": "inbox"})
		return
	}
	// Nobody is running it: resume here with the message as the prompt.
	if err := s.host(info.Path, harness.Options{Project: s.Project, Interactive: true, Verbose: s.Verbose, Prompt: b.Text}, b.Config, b.Preset, true); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"delivered": "resumed"})
}

func (s *Server) postAnswer(w http.ResponseWriter, r *http.Request) {
	info, err := s.resolve(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	b, err := readBody(r)
	if err != nil || strings.TrimSpace(b.Text) == "" {
		writeErr(w, 400, errors.New("text is required"))
		return
	}
	if alive(info.Path) || s.isHosted(info.ID) {
		if err := harness.PostInbox(info.Path, harness.InboxMessage{Type: "answer", Text: b.Text, QuestionID: b.QuestionID, From: "web"}); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, map[string]any{"delivered": "inbox"})
		return
	}
	if err := s.host(info.Path, harness.Options{Project: s.Project, Interactive: true, Verbose: s.Verbose, Answer: b.Text}, b.Config, b.Preset, true); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"delivered": "resumed"})
}

func (s *Server) postStop(w http.ResponseWriter, r *http.Request) {
	info, err := s.resolve(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if !alive(info.Path) && !s.isHosted(info.ID) {
		// A stop for a session nobody runs would sit in the inbox and quit
		// the next resume before its first tick.
		writeErr(w, 409, errors.New("session is not running"))
		return
	}
	if err := harness.PostInbox(info.Path, harness.InboxMessage{Type: "stop", From: "web"}); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) postResume(w http.ResponseWriter, r *http.Request) {
	info, err := s.resolve(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if alive(info.Path) || s.isHosted(info.ID) {
		writeErr(w, 409, errors.New("session is already running"))
		return
	}
	b, _ := readBody(r)
	if err := s.host(info.Path, harness.Options{Project: s.Project, Interactive: true, Verbose: s.Verbose, Prompt: b.Text}, b.Config, b.Preset, true); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	b, err := readBody(r)
	if err != nil || strings.TrimSpace(b.Prompt) == "" {
		writeErr(w, 400, errors.New("prompt is required"))
		return
	}
	bundle, preset := b.Config, b.Preset
	if b.UseProject {
		bundle, preset = "-", "-" // the project's own configuration, ignoring the server's --preset/--config
	}
	id, err := s.hostNew(harness.Options{Project: s.Project, Interactive: true, Verbose: s.Verbose, Prompt: b.Prompt}, bundle, preset)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) isHosted(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[id]
	return ok
}

func (s *Server) loadConfig(bundle, preset string) (config.Config, error) {
	// "-" means the project's own configuration: no server-wide default.
	if bundle == "-" {
		bundle = ""
	} else if bundle == "" {
		bundle = s.Bundle
	}
	if preset == "-" {
		preset = ""
	} else if preset == "" {
		preset = s.Preset
	}
	return config.LoadBundle(s.Project, preset, bundle)
}

// hostNew starts a new session in this process.
func (s *Server) hostNew(opts harness.Options, bundle, preset string) (string, error) {
	cfg, err := s.loadConfig(bundle, preset)
	if err != nil {
		return "", err
	}
	ui := newWebUI(s.logf)
	rt, err := harness.New(cfg, opts, ui)
	if err != nil {
		return "", err
	}
	s.run(rt, ui)
	return rt.SessionID(), nil
}

// HostSession starts a new interactive session in this process with the
// server's default configuration, the way the browser's "new session" does;
// dir, when set, is where its commands start.
func (s *Server) HostSession(prompt, dir string) (string, error) {
	return s.hostNew(harness.Options{Project: s.Project, Interactive: true, Verbose: s.Verbose, Prompt: prompt, StartDir: dir}, "", "")
}

// RegisterSessionStarter makes sessions requested from the phone run in this
// process, where they stay interactive and the UI shows them. It returns a
// function that restores the previous starter.
func (s *Server) RegisterSessionStarter() func() {
	return integration.SetSessionStarter(func(ctx context.Context, project string, req integration.SessionRequest) (integration.SessionStart, error) {
		if project != s.Project {
			return integration.SessionStart{}, fmt.Errorf("this eagent serves %s, not %s", s.Project, project)
		}
		id, err := s.HostSession(req.Prompt, req.Dir)
		if err != nil {
			return integration.SessionStart{}, err
		}
		return integration.SessionStart{ID: id, Host: "in_process", Interactive: true, Message: "Running in the eagent server; keep talking to it from this thread."}, nil
	})
}

// host resumes an existing session in this process.
func (s *Server) host(path string, opts harness.Options, bundle, preset string, _ bool) error {
	cfg, err := s.loadConfig(bundle, preset)
	if err != nil {
		return err
	}
	ui := newWebUI(s.logf)
	rt, err := harness.Resume(cfg, opts, ui, path)
	if err != nil {
		return err
	}
	s.run(rt, ui)
	return nil
}

func (s *Server) run(rt *harness.Runtime, ui *webUI) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &hosted{rt: rt, ui: ui, cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.running[rt.SessionID()] = h
	s.mu.Unlock()
	s.logf("web: session %s running in this process", rt.SessionID())
	go func() {
		h.code = rt.Run(ctx)
		s.mu.Lock()
		delete(s.running, rt.SessionID())
		s.mu.Unlock()
		close(h.done)
		s.logf("web: session %s ended (%s)", rt.SessionID(), rt.State().EndReason)
	}()
}

// Shutdown stops hosted sessions cleanly.
func (s *Server) Shutdown(timeout time.Duration) {
	s.mu.Lock()
	hs := make([]*hosted, 0, len(s.running))
	for _, h := range s.running {
		hs = append(hs, h)
	}
	s.mu.Unlock()
	for _, h := range hs {
		h.cancel()
	}
	deadline := time.After(timeout)
	for _, h := range hs {
		select {
		case <-h.done:
		case <-deadline:
			return
		}
	}
}

// ---- configuration ----------------------------------------------------------

type bundleView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Models      string `json:"models"`
	Active      bool   `json:"active"`
	Invalid     string `json:"invalid,omitempty"`
}

type promptView struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

func modelsLine(c config.Config) string {
	short := func(m string) string {
		if i := strings.LastIndexByte(m, '/'); i >= 0 {
			return m[i+1:]
		}
		return m
	}
	return short(c.Orchestrator.Model) + " / " + short(c.Task.Model) + " / " + short(c.Narrator.Model)
}

// saveBundle writes a named configuration from the UI. The body carries the
// name, a description, and either a full config object or a preset/bundle to
// copy.
func (s *Server) saveBundle(w http.ResponseWriter, r *http.Request) {
	var b settings.BundleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil || strings.TrimSpace(b.Name) == "" {
		writeErr(w, 400, errors.New("name is required"))
		return
	}
	path, err := s.settingsService().SaveBundle(r.Context(), b)
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	writeJSON(w, map[string]any{"path": path, "name": b.Name})
}

// promptName resolves a URL segment to one of the known prompt files, so no
// handler ever joins an arbitrary segment onto the prompts directory.
func promptName(raw string) (string, error) { return settings.PromptName(raw) }

func (s *Server) getPrompt(w http.ResponseWriter, r *http.Request) {
	name, err := promptName(r.PathValue("name"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	set, err := prompts.Load(s.Project)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	text := set.Text(name)
	if text == "" {
		writeErr(w, 404, fmt.Errorf("unknown prompt %s", name))
		return
	}
	writeJSON(w, map[string]any{"name": name, "source": set.Source[name], "text": text})
}

// ---- hosted-session UI ------------------------------------------------------

// webUI implements harness.UI for sessions hosted by the server. Everything
// user-visible is already in the log, so it only keeps the live status.
type webUI struct {
	mu   sync.Mutex
	st   harness.Status
	logf func(string, ...any)
}

func newWebUI(logf func(string, ...any)) *webUI { return &webUI{logf: logf} }

func (u *webUI) Narrate(string)                        {}
func (u *webUI) Ask(string, string, []string)          {}
func (u *webUI) Trace(event.Event)                     {}
func (u *webUI) Stream(string, string, string, string) {}
func (u *webUI) Input() <-chan string                  { return nil }
func (u *webUI) Idle(bool)                             {}
func (u *webUI) Log(format string, args ...any)        { u.logf("  "+format, args...) }
func (u *webUI) Status(s harness.Status) {
	u.mu.Lock()
	u.st = s
	u.mu.Unlock()
}
func (u *webUI) status() harness.Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

// ListenAndServe runs the server until ctx is cancelled.
func ListenAndServe(ctx context.Context, addr string, s *Server) error {
	stopPublisher := integration.StartPublisher(ctx, s.Project, harness.Version, s.logf)
	defer stopPublisher()
	restoreStarter := s.RegisterSessionStarter()
	defer restoreStarter()
	stopConnector := integration.StartConnector(ctx, s.Project, s.logf)
	defer stopConnector()
	stopWatcher := integration.StartResumeWatcher(ctx, s.Project, s, s.logf)
	defer stopWatcher()
	s.addr = addr
	srv := &http.Server{Addr: addr, Handler: s.loopbackOnly(s.Handler()), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		s.Shutdown(10 * time.Second)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// ---- attachments ----------------------------------------------------------------

// getAttachment serves a file from a session's attachments directory.
// loopbackOnly refuses any request whose peer is not this machine. The page
// hands its token to whoever asks for it, so the loopback boundary is what
// actually gates session creation, and sessions run shell commands.
func (s *Server) loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if ip := net.ParseIP(strings.Trim(host, "[]")); ip == nil || !ip.IsLoopback() {
			http.Error(w, "eagent serves local clients only", http.StatusForbidden)
			return
		}
		// A rebound DNS name resolves to 127.0.0.1, so the peer check alone
		// would let a remote page read the whole log; the Host must be ours
		// on reads too, not only on the mutating routes the guard covers.
		if !s.hostAllowed(r.Host) {
			http.Error(w, "request Host is not this server", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) getAttachment(w http.ResponseWriter, r *http.Request) {
	info, err := s.resolve(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	name := filepath.Base(r.PathValue("name"))
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		writeErr(w, 400, errors.New("bad attachment name"))
		return
	}
	path := filepath.Join(info.Path, "attachments", name)
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, 404, errors.New("no such attachment"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		writeErr(w, 404, errors.New("no such attachment"))
		return
	}
	// Attachments are inert: pictures, PDFs, and plain text display inline,
	// everything else downloads, and nothing may run as this origin. A file
	// the agent wrote (or the phone sent) must never become a page here.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	ct, disp := "application/octet-stream", "attachment"
	switch mt := strings.SplitN(mime.TypeByExtension(strings.ToLower(filepath.Ext(name))), ";", 2)[0]; mt {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "application/pdf", "text/plain":
		ct, disp = mt, "inline"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, url.PathEscape(name)))
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// ---- prompt overrides --------------------------------------------------------

func (s *Server) putPrompt(w http.ResponseWriter, r *http.Request) {
	name, err := promptName(r.PathValue("name"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	var b struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil || strings.TrimSpace(b.Text) == "" {
		writeErr(w, 400, errors.New("text is required"))
		return
	}
	path, err := s.settingsService().SavePrompt(r.Context(), name, b.Text, false)
	if err != nil {
		writeSettingsError(w, err)
		return
	}
	writeJSON(w, map[string]any{"name": name, "source": path})
}

func (s *Server) deletePrompt(w http.ResponseWriter, r *http.Request) {
	name, err := promptName(r.PathValue("name"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if _, err := s.settingsService().SavePrompt(r.Context(), name, "", true); err != nil {
		writeSettingsError(w, err)
		return
	}
	writeJSON(w, map[string]any{"name": name, "source": "built-in"})
}
