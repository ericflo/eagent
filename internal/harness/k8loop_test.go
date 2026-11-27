package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// The sibling 85bd57d left. A batch session (no terminal) stops its agent:
// nothing keeps it open any more. The look that stopped it has its first
// call answered and the next one rate-limited, and so does every look
// after it (each on the failed look's own wake: TurnEnd "error"). The
// closing word is never asked (the wake is always pending), so the
// session is never "ending" (stillClosing) and never down
// (lastFailedAnswered): it pays for an answered call on every retry, up to
// one an hour, for as long as the process lives. 85bd57d ends the same
// session as down when the same failures come one look later (after its
// closing word was asked).
func TestSR6LoopABatchSessionAboutToEndWhoseLooksFailAfterALookupNeverEnds(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Millisecond})
	setFor(t, &wakeSettle, time.Millisecond)
	var after atomic.Int32
	var rt atomic.Pointer[Runtime]
	var stopped atomic.Bool
	model := audit2Model(t, func(_ string, m []map[string]any) reply {
		switch {
		case stopped.Load() && m[len(m)-1]["role"] == "tool":
			after.Add(1)
			return k7RateLimited
		case stopped.Load():
			after.Add(1)
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
		case m[len(m)-1]["role"] == "tool":
			return hold()
		}
		stopped.Store(true)
		return stopsAndReports("The build is green; stopping Codex.")
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: "@e check the build"}, newFakeSeat("ok"), model, func(r *Runtime) {
		r.models[event.ActorEagent].client.MaxAttempts = 1 // no 5 s waits of the client's own
		rt.Store(r)
	})
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) && after.Load() < 40 {
		select {
		case code := <-s.done:
			s.done <- code
			deadline = time.Now()
		case <-time.After(20 * time.Millisecond):
		}
	}
	select {
	case code := <-s.done:
		s.done <- code
		t.Logf("ended: exit %d after %d calls", code, after.Load())
		return
	default:
	}
	var active int
	var finalAsked bool
	r := rt.Load()
	r.sync(func() { active, finalAsked = len(r.st.ActiveRelays()), r.turns.finalAsked })
	t.Fatalf("a batch session with nothing to keep it open (%d agents running, closing word asked: %v) made %d calls after its agent was stopped, every other one rate-limited, and still runs; said %q", active, finalAsked, after.Load(), s.ui.said())
}

// A batch session that its agent keeps open, every call of which is
// rate-limited: after the second failure the user is told the model
// "refuses my calls", though a rate limit (transient, Retryable) refuses
// nothing; the calls are tried again after the wait.
func TestK8ARateLimitedModelIsNotSaidToRefuseTheCalls(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	model := audit2Model(t, func(string, []map[string]any) reply { return k7RateLimited })
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: "@e check the build"}, newFakeSeat("ok"), model, func(r *Runtime) {
		r.models[event.ActorEagent].client.MaxAttempts = 1
	})
	var told string
	r6Until(t, "the second failure's notice", func() bool {
		for _, m := range s.ui.said() {
			if strings.Contains(m, "I keep trying") {
				told = m
				return true
			}
		}
		return false
	})
	if strings.Contains(told, "refuses") || !strings.Contains(told, "api error 429") {
		t.Fatalf("told %q for a rate limit", told)
	}
}

// A batch session's closing look is rate-limited (429, transient since
// 95b3e69, so no longer the closing path's refusal). turnFailed logs
// "trying again now" (the first failure's wait is 0), and the session
// ends at once: the closing turn closed out (fallbackFinal), and nothing
// is tried again.
func TestSR6LoopAClosingLookRateLimitedSaysItTriesAgainAndEnds(t *testing.T) {
	var closing, after atomic.Int32
	var stopped atomic.Bool
	model := audit2Model(t, func(_ string, m []map[string]any) reply {
		switch {
		case closing.Load() > 0:
			after.Add(1)
			return hold()
		case strings.Contains(lastUserText(m), "The session is ending now"):
			closing.Add(1)
			return k7RateLimited
		case m[len(m)-1]["role"] == "tool":
			return hold()
		case stopped.CompareAndSwap(false, true):
			return stopsAndReports("The build is green; stopping Codex.")
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: "@e check the build"}, newFakeSeat("ok"), model, func(r *Runtime) {
		r.models[event.ActorEagent].client.MaxAttempts = 1
	})
	code := coreExit(s)
	if closing.Load() == 0 {
		t.Fatalf("setup: no closing look; exit %d", code)
	}
	var promised bool
	for _, l := range s.ui.logged() {
		if strings.Contains(l, "trying again now") {
			promised = true
		}
	}
	if promised && after.Load() == 0 {
		t.Fatalf("logged that it tries again now, then ended (exit %d) with no call after the closing look; logged %q", code, s.ui.logged())
	}
}

// 85bd57d's own path: an ending batch session's notice look is
// rate-limited twice. The second failure logs that it tries again in an
// hour, and the session ends as down at once (exit 2), with no notice and
// no retry.
func TestSR6LoopAnEndingSessionDownLogsARetryItNeverMakes(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var after atomic.Int32
	var rt atomic.Pointer[Runtime]
	var injected, stopped atomic.Bool
	model := audit2Model(t, func(_ string, m []map[string]any) reply {
		switch {
		case injected.Load():
			after.Add(1)
			return k7RateLimited
		case m[len(m)-1]["role"] == "tool":
			return hold()
		case strings.Contains(lastUserText(m), "The session is ending now"):
			if r := rt.Load(); r != nil && !injected.Load() {
				r.sync(func() {
					r.append(event.New(event.HarnessMessage, event.ActorEagent, event.HarnessMessageData{Text: "[eagent] A note of your own came due."}))
				})
				injected.Store(true)
			}
			return hold()
		case stopped.CompareAndSwap(false, true):
			return stopsAndReports("The build is green; stopping Codex.")
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: "@e check the build"}, newFakeSeat("ok"), model, func(r *Runtime) {
		r.models[event.ActorEagent].client.MaxAttempts = 1
		rt.Store(r)
	})
	code := coreExit(s)
	if code != 2 || after.Load() != 2 {
		t.Fatalf("setup: exit %d after %d calls", code, after.Load())
	}
	for _, l := range s.ui.logged() {
		if strings.Contains(l, "trying again in") {
			t.Fatalf("logged %q, then ended as down (exit %d) without trying again; said %q", l, code, s.ui.said())
		}
	}
}
