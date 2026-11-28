package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// Ports of the old wedge_test.go, chaos_test.go, network_test.go and
// hermetic_test.go.

// ---- wedge -------------------------------------------------------------------

// relayBurstSeat is a relay seat whose agent produces a burst of transcript
// observations, as a reattach backfill or a chatty agent does.
type relayBurstSeat struct {
	events chan relay.Event
	quit   chan struct{} // closed by Close; the burst stops and closes events
	once   sync.Once
	mu     sync.Mutex
	sent   []string
}

func (f *relayBurstSeat) Events() <-chan relay.Event { return f.events }
func (f *relayBurstSeat) Send(id, text, mode string) (string, error) {
	f.mu.Lock()
	f.sent = append(f.sent, text)
	f.mu.Unlock()
	return "typed", nil
}
func (f *relayBurstSeat) Interrupt() error           { return nil }
func (f *relayBurstSeat) Keys(...string) error       { return nil }
func (f *relayBurstSeat) Screen(int) (string, error) { return "", nil }
func (f *relayBurstSeat) Kill() error                { f.Close(); return nil }
func (f *relayBurstSeat) Close()                     { f.once.Do(func() { close(f.quit) }) }
func (f *relayBurstSeat) Snapshot() relay.Status {
	return relay.Status{State: "working", Since: time.Now()}
}

// Old: TestFullLoopQueueDoesNotWedgeTheSession. A user message recorded
// while the loop's queue is full (a burst of relay events) must not wedge
// the loop, and must still reach the agent.
func TestFullLoopQueueDoesNotWedgeTheSession(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := &relayBurstSeat{events: make(chan relay.Event, 256), quit: make(chan struct{})}
	old := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		return seat, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name)}, nil
	}
	defer func() { launchRelay = old }()
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	defer model.srv.Close()
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Interactive: true, Prompt: "start", Relay: "codex"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan int, 1)
	go func() { runDone <- rt.Run(ctx) }()
	waitFor(t, "the brief to reach the agent", func() bool {
		seat.mu.Lock()
		defer seat.mu.Unlock()
		return len(seat.sent) > 0
	})
	go func() {
		defer close(seat.events)
		for i := 0; ; i++ {
			if i >= 4000 {
				<-seat.quit
				return
			}
			select {
			case seat.events <- relay.Event{Kind: relay.KindProgress, Text: "step"}:
			case <-seat.quit:
				return
			}
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(rt.loop) < cap(rt.loop) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "please also run the tests", From: "web"}); err != nil {
		t.Fatal(err)
	}
	forwarded := func() bool {
		seat.mu.Lock()
		defer seat.mu.Unlock()
		for _, s := range seat.sent {
			if strings.Contains(s, "please also run the tests") {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(30 * time.Second); !forwarded(); {
		if time.Now().After(deadline) {
			t.Fatalf("the message never reached the agent (loop queue %d/%d)", len(rt.loop), cap(rt.loop))
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the session did not stop")
	}
}

// ---- chaos -------------------------------------------------------------------

// relayChaos wraps a scripted brain with random provider faults: 5xx, 429,
// truncated streams, malformed arguments, empty replies, garbage, latency.
type relayChaos struct {
	srv   *httptest.Server
	mu    sync.Mutex
	rng   *rand.Rand
	rate  float64
	brain func(model string, msgs []map[string]any) reply
	stats map[string]int
}

func newRelayChaos(seed int64, rate float64, brain func(string, []map[string]any) reply) *relayChaos {
	c := &relayChaos{rng: rand.New(rand.NewSource(seed)), rate: rate, brain: brain, stats: map[string]int{}}
	c.srv = httptest.NewServer(http.HandlerFunc(c.handle))
	return c
}

func (c *relayChaos) handle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string           `json:"model"`
		Messages []map[string]any `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	c.mu.Lock()
	fault := ""
	if c.rng.Float64() < c.rate {
		fault = []string{"503", "429", "truncate", "malformed", "empty", "slow", "garbage"}[c.rng.Intn(7)]
	}
	c.stats[fault]++
	c.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	switch fault {
	case "503":
		w.WriteHeader(503)
		fmt.Fprint(w, `{"error":{"message":"no available server"}}`)
		return
	case "429":
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
		return
	case "truncate":
		// Dies mid tool call: no finish_reason.
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"send_message","arguments":"{\"te"}}]}}]}`)
		return
	case "garbage":
		fmt.Fprint(w, "data: {not json\n\ndata: [DONE]\n\n")
		return
	case "empty":
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	case "slow":
		time.Sleep(300 * time.Millisecond)
	default:
		// Real providers take time; without this, kills never land mid-work.
		c.mu.Lock()
		d := time.Duration(20+c.rng.Intn(60)) * time.Millisecond
		c.mu.Unlock()
		time.Sleep(d)
	}
	rep := c.brain(body.Model, body.Messages)
	if fault == "malformed" && len(rep.calls) > 0 {
		a := string(rep.calls[0].Args)
		rep.calls[0].Args = json.RawMessage(a[:len(a)/2])
	}
	if rep.text != "" {
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": rep.text}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
	}
	for i, tc := range rep.calls {
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": string(tc.Args)},
		}}}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
	}
	finish := "stop"
	if len(rep.calls) > 0 {
		finish = "tool_calls"
	}
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}})
	fmt.Fprintf(w, "data: %s\n\n", chunk)
	usage, _ := json.Marshal(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 10}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
}

func (c *relayChaos) faults() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]int{}
	for k, v := range c.stats {
		out[k] = v
	}
	return out
}

// relayChaosBrain drives a relay session to its end whatever happens: when
// the agent has reported, eagent tells the user and stops the agent; at the
// end it gives the closing word.
func relayChaosBrain(_ string, msgs []map[string]any) reply {
	all, tail := allText(msgs), lastUserText(msgs)
	switch {
	case msgs[len(msgs)-1]["role"] == "tool":
		return hold()
	case strings.Contains(tail, "The session is ending now"):
		return say("Codex wrote all three parts; nothing is left open.")
	case strings.Contains(all, "finished a turn") && !strings.Contains(all, "Stopped Codex"):
		return reply{calls: []event.ToolCall{
			tc("send_message", `{"text":"Codex wrote the three parts; stopping it."}`),
			tc("relay_control", `{"action":"stop","reason":"the work is done"}`),
		}}
	}
	return hold()
}

// relayCheckLog verifies the structural invariants of a session directory:
// every file parses, sequence numbers have no gaps or duplicates, and once
// the session ended no tool call is left without a result.
func relayCheckLog(t *testing.T, path string) *state.State {
	t.Helper()
	evs, err := store.Read(path)
	if err != nil {
		t.Fatalf("log unreadable: %v", err)
	}
	var last int64
	for _, ev := range evs {
		if ev.Seq != last+1 {
			t.Fatalf("sequence gap or duplicate: %d after %d (%s)", ev.Seq, last, ev.Source)
		}
		last = ev.Seq
	}
	st := state.Replay(evs)
	if st.Ended {
		if d := st.DanglingCalls(event.ActorEagent); len(d) > 0 {
			t.Fatalf("eagent has %d dangling tool calls after the session ended", len(d))
		}
	}
	return st
}

// relayBriefSends counts how often the brief was sent to an agent.
func relayBriefSends(st *state.State, text string) int {
	n := 0
	for _, ev := range st.Events {
		if ev.Type == event.RelaySend {
			var d event.RelaySendData
			if ev.Decode(&d) == nil && d.Text == text {
				n++
			}
		}
	}
	return n
}

const relayChaosBrief = "build the three parts"

// Old: TestChaosProviderFaults. With the provider misbehaving at random the
// session still finishes, the log stays consistent, and the user hears the
// outcome. (The old work was delegated to task workers, which are gone; the
// work here is the agent's, and the brief must reach it exactly once.)
func TestChaosProviderFaults(t *testing.T) {
	if testing.Short() {
		t.Skip("chaos suite skipped with -short")
	}
	old := errorRetryDelays
	errorRetryDelays = []time.Duration{20 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond}
	t.Cleanup(func() { errorRetryDelays = old })
	for seed := int64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			t.Setenv("EAGENT_TEST_KEY", "x")
			useRelayFactory(t, "Wrote part1.txt, part2.txt and part3.txt.")
			c := newRelayChaos(seed, 0.3, relayChaosBrain)
			defer c.srv.Close()
			ui := &fakeUI{}
			rt, err := New(testConfig(c.srv.URL), Options{Project: t.TempDir(), Prompt: relayChaosBrief}, ui)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			code := rt.Run(ctx)
			st := relayCheckLog(t, rt.sess.Path)
			if code != 0 || st.EndReason != "done" {
				t.Fatalf("exit %d reason %s; faults %v; logs %v", code, st.EndReason, c.faults(), ui.logged())
			}
			if n := relayBriefSends(st, relayChaosBrief); n != 1 {
				t.Fatalf("the brief went to the agent %d times", n)
			}
			if len(ui.said()) == 0 {
				t.Fatal("user heard nothing")
			}
			t.Logf("seed %d: faults injected %v, eagent calls %d, events %d", seed, c.faults(), st.Calls[event.ActorEagent], len(st.Events))
		})
	}
}

// TestChaosCrashResumeLoop (old chaos_test.go) is ported in
// gaps_relay_test.go: it fails on this branch (see the ledger).

// ---- network -----------------------------------------------------------------

// Old: TestModelCallsWaitForTheNetwork. A provider the network cannot reach
// is waited for, not given up on: the call goes through once the network
// is back, with no restart.
func TestModelCallsWaitForTheNetwork(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	old := offlineWait
	offlineWait = 20 * time.Millisecond
	defer func() { offlineWait = old }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens: connection refused, as with the network down
	useFakeRelays(t, newFakeSeat("ok"))
	rt, err := New(testConfig("http://"+addr), Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	rt.models[event.ActorEagent].client.MaxAttempts = 1
	relayServeLoop(t, rt)
	model := newScripted(func(string, []map[string]any) reply { return reply{text: "back"} })
	model.srv.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		time.Sleep(300 * time.Millisecond)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Error(err)
			return
		}
		srv := httptest.NewUnstartedServer(model.srv.Config.Handler)
		srv.Listener = l
		srv.Start()
		<-stop
		srv.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := rt.complete(ctx, event.ActorEagent, llm.Request{Messages: []llm.Message{{Role: "user", Text: "hi"}}}, nil)
	if err != nil || resp.Text != "back" {
		t.Fatalf("call after the network came back: %v %+v", err, resp)
	}
}

// Old: TestPhoneMirrorSurvivesAnOutageAtStart. FinaleChat failing when a
// session starts (5xx for a few seconds) delays the phone mirror; it does
// not switch it off, and a reply from the phone still reaches the session.
func TestPhoneMirrorSurvivesAnOutageAtStart(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	// Each try waits this much longer than the one before: 10, 20, 30 ms...
	setFor(t, &phoneRetryStep, 10*time.Millisecond)
	useFakeRelays(t, newFakeSeat("ok"))
	project := t.TempDir()
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	fp := newRelayPhone()
	t.Cleanup(fp.srv.Close)
	var down atomic.Bool
	var refused atomic.Int32 // first calls answered 503
	down.Store(true)
	inner := fp.srv.Config.Handler
	fp.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			if r.URL.Path == "/api/v1/me" {
				refused.Add(1)
			}
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"upstream down"}}`))
			return
		}
		inner.ServeHTTP(w, r)
	})
	ui := &fakeUI{input: make(chan string, 1)}
	rt, err := New(relayPhoneConfig(t, project, model.srv.URL, fp), Options{Project: project, Interactive: true, Prompt: "hello"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	// More tries than the old three, which then switched the mirror off.
	waitFor(t, "five tries of the first call", func() bool { return refused.Load() >= 5 })
	down.Store(false)
	for deadline := time.Now().Add(30 * time.Second); fp.postCount() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the mirror never posted after FinaleChat came back")
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	fp.userReply("this comes from the phone")
	delivered := func() bool {
		ok := false
		rt.sync(func() {
			for _, ev := range rt.st.Events {
				var d event.UserMessageData
				if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Source == "finalechat" {
					ok = true
				}
			}
		})
		return ok
	}
	for deadline := time.Now().Add(30 * time.Second); !delivered(); {
		if time.Now().After(deadline) {
			var off bool
			rt.sync(func() { off = rt.phone == nil })
			t.Fatalf("the phone reply never reached the session (mirror off: %v)", off)
		}
		time.Sleep(100 * time.Millisecond)
	}
	ui.input <- "/quit"
	<-done
}

// Old: TestOrchestratorRetriesByItselfAfterFailures. After model calls keep
// failing (a provider outage that answers 5xx), eagent tries again by
// itself; no resume or restart needed.
func TestEagentRetriesByItselfAfterFailures(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	old := errorRetryDelays
	errorRetryDelays = []time.Duration{200 * time.Millisecond}
	defer func() { errorRetryDelays = old }()
	useFakeRelays(t, newFakeSeat("Done."))
	var calls atomic.Int32
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		if msgs[len(msgs)-1]["role"] != "tool" && strings.Contains(lastUserText(msgs), "finished a turn") {
			return say("recovered: Codex is done.")
		}
		return hold()
	})
	t.Cleanup(model.srv.Close)
	inner := model.srv.Config.Handler
	model.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) <= 2 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		inner.ServeHTTP(w, r)
	})
	ui := &fakeUI{input: make(chan string, 1)}
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Interactive: true, Prompt: "do it"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	rt.models[event.ActorEagent].client.MaxAttempts = 1
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	relaySaid(t, ui, "recovered: Codex is done.")
	if n := calls.Load(); n < 3 {
		t.Fatalf("only %d calls", n)
	}
	ui.input <- "/quit"
	<-done
}

// ---- hermetic ----------------------------------------------------------------

// Old: TestHarnessSessionPublishesNothingOutward. A full phone-mirror
// session, run the way a leaking test once did, talks to the fake phone and
// nothing else: the artifact publisher leaves no trace and no non-loopback
// request is attempted.
func TestHarnessSessionPublishesNothingOutward(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	deniedBefore := testDeniedHTTP.Load()
	useFakeRelays(t, newFakeSeat("Nothing to do."))
	project := t.TempDir()
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "finished a turn") && !strings.Contains(all, "Stopped Codex"):
			return reply{calls: []event.ToolCall{
				tc("send_message", `{"text":"Nothing to do."}`),
				tc("relay_control", `{"action":"stop","reason":"nothing to do"}`),
			}}
		}
		return hold()
	})
	t.Cleanup(model.srv.Close)
	fp := newRelayPhone()
	t.Cleanup(fp.srv.Close)
	relayRunBatch(t, relayPhoneConfig(t, project, model.srv.URL, fp), Options{Project: project, Prompt: "idle"})
	if n := fp.postCount(); n == 0 {
		t.Fatal("the mirror never reached the fake phone; the test proved nothing")
	}
	var traces []string
	root := filepath.Join(project, ".agents", "eagent", "finalechat-state")
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if name := d.Name(); name == "publication.json" || name == "scan.lock" {
			rel, _ := filepath.Rel(project, path)
			traces = append(traces, rel)
		}
		return nil
	})
	if len(traces) != 0 {
		t.Fatalf("the artifact publisher ran during a harness test: %v", traces)
	}
	if n := testDeniedHTTP.Load() - deniedBefore; n != 0 {
		t.Fatalf("%d non-loopback HTTP requests were attempted during the session", n)
	}
}
