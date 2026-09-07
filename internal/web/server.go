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
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

//go:embed static/*
var static embed.FS

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
	s := &Server{Project: project, running: map[string]*hosted{}, logf: logf}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	sub, _ := fs.Sub(static, "static")
	files := http.FileServer(http.FS(sub))
	index, _ := static.ReadFile("static/index.html")
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
	s.mux.HandleFunc("POST /api/sessions", s.startSession)
	s.mux.HandleFunc("GET /api/sessions/{id}", s.getSession)
	s.mux.HandleFunc("GET /api/sessions/{id}/events", s.getEvents)
	s.mux.HandleFunc("GET /api/sessions/{id}/stream", s.stream)
	s.mux.HandleFunc("POST /api/sessions/{id}/message", s.postMessage)
	s.mux.HandleFunc("POST /api/sessions/{id}/answer", s.postAnswer)
	s.mux.HandleFunc("POST /api/sessions/{id}/stop", s.postStop)
	s.mux.HandleFunc("POST /api/sessions/{id}/resume", s.postResume)
	s.mux.HandleFunc("GET /api/config", s.getConfig)
	s.mux.HandleFunc("POST /api/config/bundles", s.saveBundle)
	s.mux.HandleFunc("GET /api/prompts/{name}", s.getPrompt)
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
func alive(sessionPath string) bool {
	f, err := os.OpenFile(filepath.Join(sessionPath, ".lock"), os.O_RDWR, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// ---- sessions ---------------------------------------------------------------

// SessionSummary is what the list and detail endpoints return.
type SessionSummary struct {
	ID           string            `json:"id"`
	Started      time.Time         `json:"started"`
	Modified     time.Time         `json:"modified"`
	Status       string            `json:"status"` // running | idle | done | awaiting-input | interrupted | quit | error
	Alive        bool              `json:"alive"`
	Hosted       bool              `json:"hosted"` // running inside this server
	FirstMessage string            `json:"first_message"`
	Models       map[string]string `json:"models"`
	Config       string            `json:"config,omitempty"`
	Subsessions  int               `json:"subsessions"`
	Size         int64             `json:"size"`
	Events       int               `json:"events"`
	Cwd          string            `json:"cwd,omitempty"`
}

// SessionDetail adds the derived runtime picture.
type SessionDetail struct {
	SessionSummary
	Tasks       []TaskView      `json:"tasks"`
	Procs       []ProcView      `json:"procs"`
	Schedules   []ScheduleView  `json:"schedules"`
	Question    *state.Question `json:"question,omitempty"`
	Idle        bool            `json:"idle"`
	Done        bool            `json:"done"`
	LastReason  string          `json:"last_reason,omitempty"`
	Usage       []UsageView     `json:"usage"`
	Context     int             `json:"context_tokens"`
	Files       []string        `json:"files"`
	Live        *harness.Status `json:"live,omitempty"`
	LastSeq     int64           `json:"last_seq"`
	Interactive bool            `json:"interactive"`
}

type TaskView struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Kind    string    `json:"kind"`
	Status  string    `json:"status"`
	Summary string    `json:"summary"`
	Turns   int       `json:"turns"`
	Created time.Time `json:"created"`
	Ended   time.Time `json:"ended,omitempty"`
	Usage   UsageView `json:"usage"`
}

type ProcView struct {
	Handle   string    `json:"handle"`
	Actor    string    `json:"actor"`
	Task     string    `json:"task,omitempty"`
	Command  string    `json:"command"`
	Status   string    `json:"status"`
	ExitCode int       `json:"exit_code"`
	Started  time.Time `json:"started"`
}

type ScheduleView struct {
	ID    string    `json:"id"`
	Kind  string    `json:"kind"`
	Spec  string    `json:"spec"`
	Note  string    `json:"note"`
	Next  time.Time `json:"next"`
	Fires int       `json:"fires"`
}

type UsageView struct {
	Actor      string  `json:"actor"`
	Calls      int     `json:"calls"`
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Cached     int     `json:"cached"`
	Reasoning  int     `json:"reasoning"`
	CacheRatio float64 `json:"cache_ratio"`
}

func usageView(actor string, calls int, u event.Usage) UsageView {
	v := UsageView{Actor: actor, Calls: calls, Input: u.Input, Output: u.Output, Cached: u.Cached, Reasoning: u.Reasoning}
	if u.Input > 0 {
		v.CacheRatio = float64(u.Cached) / float64(u.Input)
	}
	return v
}

func (s *Server) summarize(info store.Info) (SessionSummary, *state.State, error) {
	evs, err := store.Read(info.Path)
	if err != nil {
		return SessionSummary{}, nil, err
	}
	st := state.Replay(evs)
	sum := SessionSummary{
		ID: info.ID, Started: info.Started, Modified: info.Modified, Subsessions: info.Subsessions, Size: info.Size,
		Models: st.Models, Cwd: st.Cwd, Events: len(evs),
	}
	if len(evs) > 0 {
		var d event.SessionStartData
		_ = evs[0].Decode(&d)
		sum.Config = d.Config
	}
	for _, ev := range evs {
		if ev.Type == event.UserMessage {
			var d event.UserMessageData
			_ = ev.Decode(&d)
			sum.FirstMessage = d.Text
			break
		}
	}
	s.mu.Lock()
	_, hosted := s.running[info.ID]
	s.mu.Unlock()
	sum.Hosted = hosted
	sum.Alive = hosted || alive(info.Path)
	switch {
	case sum.Alive && st.Idle() && st.Question != nil:
		sum.Status = "awaiting-input"
	case sum.Alive && st.Idle():
		sum.Status = "idle"
	case sum.Alive:
		sum.Status = "running"
	case st.Ended:
		sum.Status = st.EndReason
	default:
		sum.Status = "interrupted"
	}
	return sum, st, nil
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
	sum, st, err := s.summarize(info)
	if err != nil {
		return nil, err
	}
	d := &SessionDetail{SessionSummary: sum, Idle: st.Idle(), LastSeq: st.LastSeq(), Context: st.ContextTokens(event.ActorOrchestrator), Interactive: st.Interactive}
	if st.LastYield != nil {
		d.Done = st.LastYield.Done
		d.LastReason = st.LastYield.Reason
	}
	d.Question = st.Question
	for _, id := range st.TaskOrder {
		t := st.Tasks[id]
		d.Tasks = append(d.Tasks, TaskView{ID: t.ID, Title: t.Title, Kind: t.Kind, Status: t.Status, Summary: t.Summary, Turns: t.Turns, Created: t.Created, Ended: t.Ended, Usage: usageView(event.ActorTask, t.Turns, t.Usage)})
	}
	for _, h := range st.ProcOrder {
		p := st.Procs[h]
		d.Procs = append(d.Procs, ProcView{Handle: p.Handle, Actor: p.Actor, Task: p.Task, Command: p.Command, Status: p.Status, ExitCode: p.ExitCode, Started: p.Started})
	}
	for _, sc := range st.ActiveSchedules() {
		d.Schedules = append(d.Schedules, ScheduleView{ID: sc.ID, Kind: sc.Kind, Spec: sc.Spec, Note: sc.Note, Next: sc.Next, Fires: sc.Fires})
	}
	for _, a := range []string{event.ActorOrchestrator, event.ActorTask, event.ActorNarrator} {
		d.Usage = append(d.Usage, usageView(a, st.Calls[a], st.Totals[a]))
	}
	for _, ss := range st.Subsessions {
		d.Files = append(d.Files, ss.File)
	}
	s.mu.Lock()
	if h := s.running[info.ID]; h != nil {
		live := h.ui.status()
		d.Live = &live
	}
	s.mu.Unlock()
	if d.Tasks == nil {
		d.Tasks = []TaskView{}
	}
	if d.Procs == nil {
		d.Procs = []ProcView{}
	}
	if d.Schedules == nil {
		d.Schedules = []ScheduleView{}
	}
	return d, nil
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
	emitStatus := func() {
		if d, err := s.detail(info); err == nil {
			send("status", d)
		}
	}
	emitStatus()
	tail := newTailer(info.Path, after)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			for _, ev := range tail.next() {
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
	id, err := s.hostNew(harness.Options{Project: s.Project, Interactive: true, Verbose: s.Verbose, Prompt: b.Prompt}, b.Config, b.Preset)
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
	if bundle == "" {
		bundle = s.Bundle
	}
	if preset == "" {
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

type configView struct {
	Effective config.Config     `json:"effective"`
	Presets   []bundleView      `json:"presets"`
	Bundles   []bundleView      `json:"bundles"`
	Prompts   []promptView      `json:"prompts"`
	Keys      map[string]bool   `json:"keys"` // env var -> present
	Project   string            `json:"project"`
	Files     map[string]string `json:"files"`
}

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

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	bundle := r.URL.Query().Get("bundle")
	preset := r.URL.Query().Get("preset")
	cfg, err := s.loadConfig(bundle, preset)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	v := configView{Effective: cfg, Project: s.Project, Keys: map[string]bool{}, Files: map[string]string{}}
	for _, name := range config.PresetNames() {
		c := config.Defaults()
		if apply, ok := config.Presets[name]; ok {
			apply(&c)
		}
		v.Presets = append(v.Presets, bundleView{Name: name, Description: config.PresetDescription(name), Models: modelsLine(c), Active: cfg.Name == "" && (cfg.Preset == name || (cfg.Preset == "" && name == "glm"))})
	}
	names, _ := config.ListBundles(s.Project)
	for _, name := range names {
		bv := bundleView{Name: name, Active: cfg.Name == name}
		if c, err := config.LoadBundle(s.Project, "", name); err != nil {
			bv.Invalid = err.Error()
		} else {
			bv.Description, bv.Models = c.Description, modelsLine(c)
		}
		v.Bundles = append(v.Bundles, bv)
	}
	for _, env := range []string{"TOGETHER_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", cfg.Orchestrator.APIKeyEnv, cfg.Task.APIKeyEnv, cfg.Narrator.APIKeyEnv} {
		v.Keys[env] = os.Getenv(env) != ""
	}
	if set, err := prompts.Load(s.Project); err == nil {
		for _, name := range prompts.Names {
			v.Prompts = append(v.Prompts, promptView{Name: name, Source: set.Source[name]})
		}
	}
	v.Files["config"] = config.File(s.Project)
	v.Files["bundles"] = config.BundlesDir(s.Project)
	v.Files["prompts"] = prompts.Dir(s.Project)
	if v.Bundles == nil {
		v.Bundles = []bundleView{}
	}
	sort.Slice(v.Bundles, func(i, j int) bool { return v.Bundles[i].Name < v.Bundles[j].Name })
	writeJSON(w, v)
}

// saveBundle writes a named configuration from the UI. The body carries the
// name, a description, and either a full config object or a preset/bundle to
// copy.
func (s *Server) saveBundle(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		From        string          `json:"from"` // preset or bundle name to copy, optional
		Config      json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil || strings.TrimSpace(b.Name) == "" {
		writeErr(w, 400, errors.New("name is required"))
		return
	}
	var cfg config.Config
	var err error
	if len(b.Config) > 0 && string(b.Config) != "null" {
		cfg = config.Defaults()
		if err = json.Unmarshal(b.Config, &cfg); err != nil {
			writeErr(w, 400, fmt.Errorf("config: %w", err))
			return
		}
		if err = cfg.Validate(); err != nil {
			writeErr(w, 400, err)
			return
		}
	} else {
		cfg, err = config.LoadBundle(s.Project, "", b.From)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
	}
	path, err := config.SaveBundle(s.Project, strings.TrimSpace(b.Name), strings.TrimSpace(b.Description), cfg)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, map[string]any{"path": path, "name": b.Name})
}

func (s *Server) getPrompt(w http.ResponseWriter, r *http.Request) {
	set, err := prompts.Load(s.Project)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	name := strings.ToUpper(strings.TrimSuffix(r.PathValue("name"), ".md")) + ".md"
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
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
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
