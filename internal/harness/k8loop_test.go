package harness

import (
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
