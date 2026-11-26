package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// fakeUI records what the user would see.
type fakeUI struct {
	mu       sync.Mutex
	idles    int
	messages []string
	asked    []string
	logs     []string
	input    chan string
}

func (u *fakeUI) Say(text string) {
	u.mu.Lock()
	u.messages = append(u.messages, text)
	u.mu.Unlock()
}
func (u *fakeUI) Ask(id, text string, options []string) {
	u.mu.Lock()
	u.asked = append(u.asked, text)
	u.mu.Unlock()
}
func (u *fakeUI) Status(Status)             {}
func (u *fakeUI) Trace(event.Event)         {}
func (u *fakeUI) Stream(kind, delta string) {}
func (u *fakeUI) Log(format string, args ...any) {
	u.mu.Lock()
	u.logs = append(u.logs, fmt.Sprintf(format, args...))
	u.mu.Unlock()
}
func (u *fakeUI) Input() <-chan string { return u.input }
func (u *fakeUI) Idle(on bool) {
	if on {
		u.mu.Lock()
		u.idles++
		u.mu.Unlock()
	}
}
func (u *fakeUI) askedCount() int   { u.mu.Lock(); defer u.mu.Unlock(); return len(u.asked) }
func (u *fakeUI) messageCount() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.messages) }

// answerCount is messageCount without the harness's notice that the session
// is being reread (rereadNotice): what the model said.
func (u *fakeUI) answerCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for _, m := range u.messages {
		if m != rereadNotice {
			n++
		}
	}
	return n
}
func (u *fakeUI) idleCount() int { u.mu.Lock(); defer u.mu.Unlock(); return u.idles }
func (u *fakeUI) snapshot() ([]string, []string, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string{}, u.messages...), append([]string{}, u.asked...), append([]string{}, u.logs...)
}
func (u *fakeUI) said() []string   { m, _, _ := u.snapshot(); return m }
func (u *fakeUI) logged() []string { _, _, l := u.snapshot(); return l }

// request is one call the fake model server received, as sent.
type request struct {
	Model    string
	Messages []json.RawMessage
	Tools    json.RawMessage
	// Rest is everything else in the request body (effort, output limit,
	// cache key and so on), keys sorted.
	Rest string
}

// scripted is a fake chat-completions server. brain decides each reply from
// the model name and the messages sent so far.
type scripted struct {
	srv   *httptest.Server
	brain func(model string, msgs []map[string]any) reply
	mu    sync.Mutex
	calls map[string]int
	reqs  []request
}

type reply struct {
	text   string
	calls  []event.ToolCall
	prompt int // reported prompt tokens
	block  <-chan struct{}
	status int    // an HTTP error instead of a reply
	body   string // its body
	finish string // the finish reason, when not the usual one ("length")
	// noUsage: the provider reports no sizes at all.
	noUsage bool
}

var tcSeq atomic.Int64

func tc(name string, args string) event.ToolCall {
	return event.ToolCall{ID: fmt.Sprintf("c%d", tcSeq.Add(1)), Name: name, Args: json.RawMessage(args)}
}

// hold is the reply of a model with nothing to say or do.
func hold() reply { return reply{calls: []event.ToolCall{tc("hold", `{}`)}} }

// say is a reply that tells the user something.
func say(text string) reply {
	args, _ := json.Marshal(map[string]any{"text": text})
	return reply{calls: []event.ToolCall{tc("send_message", string(args))}}
}

func newScripted(brain func(model string, msgs []map[string]any) reply) *scripted {
	s := &scripted{brain: brain, calls: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model    string            `json:"model"`
			Messages []json.RawMessage `json:"messages"`
			Tools    json.RawMessage   `json:"tools"`
		}
		_ = json.Unmarshal(raw, &body)
		msgs := make([]map[string]any, len(body.Messages))
		for i, m := range body.Messages {
			_ = json.Unmarshal(m, &msgs[i])
		}
		var rest map[string]json.RawMessage
		_ = json.Unmarshal(raw, &rest)
		delete(rest, "messages")
		delete(rest, "tools")
		restJSON, _ := json.Marshal(rest)
		s.mu.Lock()
		s.calls[body.Model]++
		s.reqs = append(s.reqs, request{Model: body.Model, Messages: body.Messages, Tools: body.Tools, Rest: string(restJSON)})
		s.mu.Unlock()
		rep := s.brain(body.Model, msgs)
		if rep.block != nil {
			select {
			case <-rep.block:
			case <-r.Context().Done():
				return
			}
		}
		if rep.status != 0 {
			w.WriteHeader(rep.status)
			fmt.Fprint(w, rep.body)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if rep.text != "" {
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": rep.text}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		for i, c := range rep.calls {
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": i, "id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(c.Args)},
			}}}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		finish := "stop"
		if len(rep.calls) > 0 {
			finish = "tool_calls"
		}
		if rep.finish != "" {
			finish = rep.finish
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		prompt := rep.prompt
		if prompt == 0 {
			prompt = 1000
		}
		usage, _ := json.Marshal(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": prompt, "completion_tokens": 10}})
		if rep.noUsage {
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
	}))
	return s
}

func (s *scripted) count(model string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[model]
}

// requests returns the calls one model received, in order.
func (s *scripted) requests(model string) []request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []request
	for _, q := range s.reqs {
		if q.Model == model {
			out = append(out, q)
		}
	}
	return out
}

// testModel is the model name eagent runs on in tests.
const testModel = "eagent-model"

func testConfig(url string) config.Config {
	cfg := config.Defaults()
	cfg.Model.BaseURL = url
	cfg.Model.APIKeyEnv = "EAGENT_TEST_KEY"
	cfg.Model.Protocol = llm.ProtocolChat
	cfg.Model.Model = testModel
	cfg.Model.Fallback = nil
	cfg.TickSeconds = 3600
	cfg.RolloverTokens = 20000
	cfg.Relay.Default = "codex"
	off := false
	cfg.Finalechat.Enabled = &off    // never the real phone from a test
	cfg.Finalechat.Artifacts = false // never the real artifact service either
	return cfg
}

// testDeniedHTTP counts HTTP requests the hermetic guard refused: anything
// not aimed at a test-local (loopback) server.
var testDeniedHTTP atomic.Int64

// loopbackOnlyTransport fails any request that would leave the machine, so
// a regression can never silently reach a production service again. The
// fake model and phone servers are httptest (127.0.0.1), so legitimate
// test traffic is unaffected.
type loopbackOnlyTransport struct{ base http.RoundTripper }

func (t loopbackOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.URL.Hostname() {
	case "127.0.0.1", "::1", "localhost":
		return t.base.RoundTrip(req)
	default:
		testDeniedHTTP.Add(1)
		return nil, fmt.Errorf("hermetic test guard: refusing non-loopback request to %s", req.URL.Host)
	}
}

// TestMain keeps every test in this package away from the machine it runs
// on. No test starts or attaches to a real agent: every session starts
// one, so the launcher is replaced with one that fails unless a test
// installs its own (useFakeRelays). No test reaches a real Finalechat
// account: the kill switch wins over any token on the machine, home is an
// empty directory, and non-loopback HTTP is refused outright; a suite that
// attempted even one such request fails.
func TestMain(m *testing.M) {
	launchRelay = func(relay.Spec, string, bool) (relaySeat, relay.Started, error) {
		return nil, relay.Started{}, errors.New("tests start no real agent")
	}
	attachRelay = func(relay.Spec, string, string, string, string, time.Time) (relaySeat, error) {
		return nil, errors.New("tests attach to no real agent")
	}
	agentInstalled = func(string) bool { return true }
	relay.OfflineCatalogs()
	wakeSettle, keysSettle = 0, 0
	// The phone's status line is written at most every 2.5 seconds; a test
	// that waits for a status would wait that out after every change.
	statusMinGap = 20 * time.Millisecond
	// The loop reads its inbox and looks at the clock every half second;
	// a test that posts to the inbox, or waits on a time the loop notices
	// only then, would wait up to that much each time.
	loopPoll = 25 * time.Millisecond
	// A minute for the closing word and 75 seconds of grace at shutdown
	// are real waits when a test's model is already gone.
	finalReportTimeout, finalReportGrace = 3*time.Second, 4*time.Second
	// Sessions record to temporary directories (t.TempDir) and fsync every
	// event; on a memory filesystem that costs nothing. All of it is
	// removed when the run ends.
	var memTmp string
	diskTmp = os.TempDir()
	if dir := memoryDisk(); dir != "" {
		// A run that was killed could not remove its directory, and here
		// that is memory: clear out any that are over an hour old.
		stale, _ := filepath.Glob(filepath.Join(dir, "eagent-harness-test*"))
		for _, d := range stale {
			if info, err := os.Stat(d); err == nil && time.Since(info.ModTime()) > time.Hour {
				os.RemoveAll(d)
			}
		}
		if d, err := os.MkdirTemp(dir, "eagent-harness-test"); err == nil {
			memTmp = d
			os.Setenv("TMPDIR", d)
		}
	}
	os.Setenv("EAGENT_FINALECHAT", "off")
	// The thread's title and summary are kept by the loop's own model, which
	// would add a call most scripts have no reply for; the tests that cover
	// them turn them back on.
	os.Setenv("EAGENT_SUMMARIES", "off")
	if home, err := os.MkdirTemp("", "eagent-hermetic-home"); err == nil {
		os.Setenv("HOME", home)
		os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		os.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
		os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	}
	os.Unsetenv("FINALECHAT_TOKEN")
	os.Unsetenv("FINALECHAT_URL")
	http.DefaultTransport = loopbackOnlyTransport{base: http.DefaultTransport}
	code := m.Run()
	if memTmp != "" {
		os.RemoveAll(memTmp)
	}
	if n := testDeniedHTTP.Load(); n != 0 {
		fmt.Fprintf(os.Stderr, "hermetic test guard: %d non-loopback HTTP requests were refused\n", n)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// lastUserText returns the text of the last user message in a request: the
// tail of the prompt.
func lastUserText(msgs []map[string]any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i]["role"] == "user" {
			s, _ := msgs[i]["content"].(string)
			return s
		}
	}
	return ""
}

func allText(msgs []map[string]any) string {
	var b strings.Builder
	for _, m := range msgs {
		switch c := m["content"].(type) {
		case string:
			b.WriteString(c + "\n")
		case []any: // text parts next to images
			for _, p := range c {
				if pm, ok := p.(map[string]any); ok {
					if t, ok := pm["text"].(string); ok {
						b.WriteString(t + "\n")
					}
				}
			}
		}
	}
	return b.String()
}

func countRole(msgs []map[string]any, role string) int {
	n := 0
	for _, m := range msgs {
		if m["role"] == role {
			n++
		}
	}
	return n
}

// fakeSeat stands in for an agent in a terminal: whatever is sent to it is
// recorded, and it answers each message with one finished turn.
type fakeSeat struct {
	mu         sync.Mutex
	sent       []string
	modes      []string
	keys       [][]string
	events     chan relay.Event
	once       sync.Once
	killed     bool
	reply      string
	screen     string
	interrupts int
	// waiting makes every reply a turn that runs nothing, as an agent
	// holding on work of its own answers.
	waiting bool
	// hang leaves the agent working: it takes the message and never ends
	// the turn.
	hang bool
	// sendErr fails every send.
	sendErr error
	// lag is how long the agent takes over a turn: its answer comes that
	// long after it took the message, not in the same instant.
	lag time.Duration
}

func newFakeSeat(reply string) *fakeSeat {
	return &fakeSeat{events: make(chan relay.Event, 256), reply: reply, screen: "› Ask Codex to do anything"}
}

func (f *fakeSeat) Events() <-chan relay.Event { return f.events }
func (f *fakeSeat) Send(id, text, mode string) (string, error) {
	f.mu.Lock()
	err := f.sendErr
	if err == nil {
		f.sent = append(f.sent, text)
		f.modes = append(f.modes, mode)
	}
	waiting, hang, lag, reply := f.waiting, f.hang, f.lag, f.reply
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	f.events <- relay.Event{Kind: relay.KindState, State: "working"}
	f.events <- relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true}
	if waiting {
		f.events <- relay.Event{Kind: relay.KindTurn, Text: reply, DurationMS: 1000}
		f.events <- relay.Event{Kind: relay.KindState, State: "idle"}
		return "typed into Codex", nil
	}
	if hang {
		return "typed into Codex", nil
	}
	turn := func() {
		f.events <- relay.Event{Kind: relay.KindProgress, Text: "Listing the open PRs."}
		f.events <- relay.Event{Kind: relay.KindActivity, Text: "running gh pr list"}
		f.events <- relay.Event{Kind: relay.KindTurn, Text: reply, DurationMS: 4000, Tools: 3, Images: []string{"/w/shot.png"}}
		f.events <- relay.Event{Kind: relay.KindState, State: "idle"}
	}
	if lag > 0 {
		time.AfterFunc(lag, turn)
	} else {
		turn()
	}
	return "typed into Codex", nil
}
func (f *fakeSeat) Interrupt() error {
	f.mu.Lock()
	f.interrupts++
	f.mu.Unlock()
	return nil
}
func (f *fakeSeat) Keys(keys ...string) error {
	f.mu.Lock()
	f.keys = append(f.keys, keys)
	f.mu.Unlock()
	return nil
}
func (f *fakeSeat) Screen(int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.screen, nil
}
func (f *fakeSeat) Kill() error            { f.mu.Lock(); f.killed = true; f.mu.Unlock(); f.Close(); return nil }
func (f *fakeSeat) Close()                 { f.once.Do(func() { close(f.events) }) }
func (f *fakeSeat) Snapshot() relay.Status { return relay.Status{State: "idle", Since: time.Now()} }
func (f *fakeSeat) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent...)
}
func (f *fakeSeat) sentModes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.modes...)
}
func (f *fakeSeat) wasKilled() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.killed }

// emit makes the agent report something on its own.
func (f *fakeSeat) emit(evs ...relay.Event) {
	for _, ev := range evs {
		f.events <- ev
	}
}

// useFakeRelays makes every agent the session starts or reattaches to the
// given seat.
func useFakeRelays(t *testing.T, seat *fakeSeat) (*[]relay.Spec, *[]string) {
	t.Helper()
	mu := &fakeRelaysMu
	var launched []relay.Spec
	var attached []string
	oldLaunch, oldAttach := launchRelay, attachRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		mu.Lock()
		launched = append(launched, spec)
		mu.Unlock()
		return seat, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	attachRelay = func(spec relay.Spec, host, target, innerID, transcript string, since time.Time) (relaySeat, error) {
		mu.Lock()
		attached = append(attached, target+"@"+since.Format(time.RFC3339Nano))
		mu.Unlock()
		return seat, nil
	}
	t.Cleanup(func() { launchRelay, attachRelay = oldLaunch, oldAttach })
	return &launched, &attached
}

// fakeRelaysMu guards the lists useFakeRelays fills: agents are started
// off the loop goroutine, so a test reads them through launchedSpecs and
// attachedTargets, never directly.
var fakeRelaysMu sync.Mutex

func launchedSpecs(p *[]relay.Spec) []relay.Spec {
	fakeRelaysMu.Lock()
	defer fakeRelaysMu.Unlock()
	return append([]relay.Spec(nil), *p...)
}

func attachedTargets(p *[]string) []string {
	fakeRelaysMu.Lock()
	defer fakeRelaysMu.Unlock()
	return append([]string(nil), *p...)
}

// setFor sets a package variable (a delay, usually) for one test and puts
// it back when the test ends.
func setFor[T any](t *testing.T, v *T, val T) {
	t.Helper()
	old := *v
	*v = val
	t.Cleanup(func() { *v = old })
}

// session is a running test session: an interactive runtime with a fake
// agent and a scripted model.
type session struct {
	t      *testing.T
	rt     *Runtime
	ui     *fakeUI
	seat   *fakeSeat
	model  *scripted
	cancel context.CancelFunc
	done   chan int

	stopOnce sync.Once
}

// startSession starts an interactive session whose agent is a fake seat and
// whose model answers from brain. tune adjusts the configuration first.
func startSession(t *testing.T, prompt string, brain func(model string, msgs []map[string]any) reply, tune func(*config.Config)) *session {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("Merged #42 and #44; main is green at c92b5bad.")
	useFakeRelays(t, seat)
	model := newScripted(brain)
	t.Cleanup(model.srv.Close)
	cfg := testConfig(model.srv.URL)
	if tune != nil {
		tune(&cfg)
	}
	ui := &fakeUI{input: make(chan string, 16)}
	rt, err := New(cfg, Options{Project: t.TempDir(), Interactive: true, Prompt: prompt}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s := &session{t: t, rt: rt, ui: ui, seat: seat, model: model, cancel: cancel, done: make(chan int, 1)}
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	return s
}

// stop ends the session and waits for it.
func (s *session) stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		select {
		case <-s.done:
		case <-time.After(20 * time.Second):
			s.t.Error("the session did not stop")
		}
	})
}

// read runs fn against the live state on the loop goroutine.
func (s *session) read(fn func(st *state.State)) {
	s.rt.sync(func() { fn(s.rt.st) })
}

// events returns the session's events of the given types (all when none).
func (s *session) events(types ...string) []event.Event {
	var out []event.Event
	s.read(func(st *state.State) {
		for _, ev := range st.Events {
			if len(types) == 0 {
				out = append(out, ev)
				continue
			}
			for _, ty := range types {
				if ev.Type == ty {
					out = append(out, ev)
				}
			}
		}
	})
	return out
}

// waitFor polls until cond holds.
func (s *session) waitFor(what string, cond func() bool) {
	s.t.Helper()
	waitFor(s.t, what, cond)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// settled waits until the loop has nothing in flight and nothing owed a
// look, nothing is queued for it and the fake agent has nothing unread,
// and all of that has held for settleQuiet. The queue and the agent's
// events are looked at directly, so the quiet only has to outlast a hop
// between goroutines (the agent's pump to the loop, a timer's post).
func (s *session) settled() {
	s.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var quietSince time.Time
	for time.Now().Before(deadline) {
		idle := false
		s.rt.sync(func() { idle = !s.rt.turns.busy && !s.rt.wakePending() && len(s.rt.later) == 0 })
		idle = idle && len(s.rt.loop) == 0 && (s.seat == nil || len(s.seat.events) == 0)
		switch {
		case !idle:
			quietSince = time.Time{}
		case quietSince.IsZero():
			quietSince = time.Now()
		case time.Since(quietSince) >= settleQuiet:
			return
		}
		time.Sleep(time.Millisecond)
	}
	s.t.Fatalf("timed out waiting for the loop to settle")
}

// settleQuiet is how long settled wants the loop to stay quiet: many times
// a goroutine hop under the race detector. It was eight polls 5ms apart
// (about 45ms a call, over 200 calls).
const settleQuiet = 20 * time.Millisecond

// type_ sends a line as the user at the terminal.
func (s *session) type_(line string) { s.ui.input <- line }

// contextWithCancel is a test-length context.
func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}
