package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/store"
)

// A run that resumes a session while the last run is still writing its
// final events waits for the lock (store's short retry) and must then read
// the record as it stands after that run let go: what was committed in the
// wait is part of the history, and the new run's events follow it in
// order, one number after another.
func TestResumingWhileTheLastRunFinishesWritingKeepsTheRecordInOrder(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()

	cfg := testConfig(s.model.srv.URL)
	resume := func() (*Runtime, error) {
		return Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 1)}, path)
	}
	// The last run: it holds the lock and has more to write.
	last, err := resume()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		rt  *Runtime
		err error
	}
	next := make(chan result, 1)
	go func() {
		rt, err := resume()
		next <- result{rt, err}
	}()
	// Well inside the lock's retry window, the last run commits its final
	// events and lets go.
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if _, err := last.sess.Append(event.New(event.Message, event.ActorEagent, event.MessageData{Text: "written while the next run waited"})); err != nil {
			t.Fatal(err)
		}
	}
	lastSeq := last.sess.LastSeq()
	last.sess.Close()

	got := <-next
	if got.err != nil {
		t.Fatalf("the next run could not take the session: %v", got.err)
	}
	defer got.rt.sess.Close()
	ev, err := got.rt.sess.Append(event.New(event.Message, event.ActorEagent, event.MessageData{Text: "the next run"}))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Seq <= lastSeq {
		t.Errorf("the next run numbered its event %d, at or below the last run's %d", ev.Seq, lastSeq)
	}
	if n := got.rt.st.LastSeq(); n < lastSeq {
		t.Errorf("the next run's state ends at %d and misses what was committed up to %d", n, lastSeq)
	}
	events, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d of the record (%s, %s:%d) is numbered %d", i+1, e.Type, e.Source.File, e.Source.Line, e.Seq)
		}
	}
}

// routeEvents counts the route switches in a runtime's record.
func routeEvents(t *testing.T, rt *Runtime) int {
	t.Helper()
	n := 0
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			if ev.Type == event.Route {
				n++
			}
		}
	})
	return n
}

// Two routes to the same model at the same address (a second key, say)
// are two routes: when the first cannot serve the call the second is
// tried, and when it cannot either the call fails. The route the call
// moved to is the one whose window the session sizes itself by.
func TestTwoRoutesToTheSameModelAreEachTriedOnce(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_KEY2", "y")
	useFakeRelays(t, newFakeSeat("ok"))
	model := newScripted(func(string, []map[string]any) reply { return reply{status: 401, body: `{"error":{"message":"bad key"}}`} })
	t.Cleanup(model.srv.Close)
	cfg := testConfig(model.srv.URL)
	cfg.Model.ContextTokens = 100_000
	cfg.Model.Fallback = &config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: model.srv.URL, Model: testModel, APIKeyEnv: "EAGENT_TEST_KEY2", ContextTokens: 300_000}
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	relayServeLoop(t, rt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = rt.complete(ctx, event.ActorEagent, llm.Request{Messages: []llm.Message{{Role: "user", Text: "hi"}}}, nil)
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Status != 401 {
		t.Fatalf("the call ended with %v, want the second route's refusal", err)
	}
	model.mu.Lock()
	calls := model.calls[testModel]
	model.mu.Unlock()
	if calls != 2 {
		t.Errorf("the provider was called %d times, want once per route", calls)
	}
	if n := routeEvents(t, rt); n != 1 {
		t.Errorf("the record has %d route switches, want 1", n)
	}
	var window int
	rt.sync(func() { window, _ = rt.window() })
	if window != 300_000 {
		t.Errorf("on the second route the window is %d, want that route's 300000", window)
	}
}

// When a call moves to a fallback, the session's live settings say so: the
// app's settings sheet shows the model that answers, and a preset saved
// from it is that model, not the route that stopped working.
func TestAFallbackTakingOverIsWhatTheLiveSettingsShow(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	model := newScripted(func(m string, _ []map[string]any) reply {
		if m == testModel {
			return reply{status: 401, body: `{"error":{"message":"bad key"}}`}
		}
		return reply{text: "here"}
	})
	t.Cleanup(model.srv.Close)
	cfg := testConfig(model.srv.URL)
	cfg.Model.ReasoningEffort = "high"
	cfg.Model.Fallback = &config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: model.srv.URL, Model: "eagent-fallback", APIKeyEnv: "EAGENT_TEST_KEY", ReasoningEffort: "low"}
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	relayServeLoop(t, rt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := rt.complete(ctx, event.ActorEagent, llm.Request{Messages: []llm.Message{{Role: "user", Text: "hi"}}}, nil); err != nil {
		t.Fatal(err)
	}
	var got runtimecontrol.Route
	rt.sync(func() { got = rt.activeSettings.Routes[runtimecontrol.RouteEagent] })
	if got.Model != "eagent-fallback" || got.Effort != "low" {
		t.Fatalf("the live settings show %+v after the fallback took over", got)
	}
	// An unrelated change from the app (same routes as shown) keeps the
	// session on the fallback.
	before := map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: got}
	var swaps []routeSwap
	rt.sync(func() { swaps, err = rt.routeChanges(rt.activeSettings.Routes, before) })
	if err != nil || len(swaps) != 0 {
		t.Fatalf("an unchanged pick swapped the route: %+v %v", swaps, err)
	}
}

// A model picked in the live settings starts clean: the old model's
// failures and their back-off, a refusal of the prompt as too long, and
// how its provider counted tokens belong to the old model.
func TestPickingAnotherModelStartsWithoutTheOldModelsTrouble(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("OPENROUTER_API_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1") // clients are swapped, never called
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	tr := &rt.turns
	tr.failures, tr.retryAt, tr.failTold = 4, time.Now().Add(time.Hour), true
	tr.overflowed, tr.futileLogged = true, true
	rt.tokenScale = 3 * scaleUnit
	before := runtimecontrol.FromConfig(cfg).Routes
	after := map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: {Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "high"}}
	swaps, err := rt.routeChanges(before, after)
	if err != nil || len(swaps) != 1 {
		t.Fatalf("pick: %+v %v", swaps, err)
	}
	rt.swapRoute(swaps[0])
	if tr.failures != 0 || !tr.retryAt.IsZero() || tr.failTold || tr.overflowed || tr.futileLogged || rt.tokenScale != 0 {
		t.Fatalf("after the swap: failures=%d retryAt=%v failTold=%v overflowed=%v futileLogged=%v tokenScale=%d",
			tr.failures, tr.retryAt, tr.failTold, tr.overflowed, tr.futileLogged, rt.tokenScale)
	}
}
