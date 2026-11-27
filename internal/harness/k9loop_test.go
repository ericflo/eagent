package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// A batch session's closing turn reads the record (session_search) while
// a notice of eagent's own comes in. Its next call, shown the notice and
// taking it as its wake, is rate-limited. afterFailure says the closing
// turn closes out (failCloseOut: the session is still "closing"), and
// turnFailed logs that the session ends without this look; but the
// notice's wake is still pending (no call that came back saw it), so
// maybeEnd does not end the session, and maybeTurn makes the same look,
// for the same notice, at once.
func TestSR7LoopAClosingLookFailedWithANoticePendingSaysItEndsThenLooksAgain(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	setFor(t, &wakeSettle, time.Millisecond)
	const note = "A note of your own came due."
	var phase, again atomic.Int32
	var shown atomic.Bool
	var rt atomic.Pointer[Runtime]
	var stopped atomic.Bool
	model := audit2Model(t, func(_ string, m []map[string]any) reply {
		switch {
		case phase.Load() == 2:
			if strings.Contains(allText(m), note) {
				again.Add(1)
			}
			return hold()
		case phase.Load() == 1:
			shown.Store(strings.Contains(allText(m), note))
			phase.Store(2)
			return k7RateLimited
		case strings.Contains(lastUserText(m), "The session is ending now"):
			if r := rt.Load(); r != nil {
				r.sync(func() {
					r.append(event.New(event.HarnessMessage, event.ActorEagent, event.HarnessMessageData{Text: "[eagent] " + note}))
				})
			}
			phase.Store(1)
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
		case m[len(m)-1]["role"] == "tool":
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
	if phase.Load() != 2 || !shown.Load() {
		t.Fatalf("setup: the closing turn's second call (shown the notice: %v) was not made; exit %d", shown.Load(), code)
	}
	var said string
	for _, l := range s.ui.logged() {
		if strings.Contains(l, "ends without this look") {
			said = l
		}
	}
	if said != "" && again.Load() > 0 {
		t.Fatalf("logged %q, then made the same look (shown the same notice) %d more times before ending (exit %d)", said, again.Load(), code)
	}
}

// A batch session past its closing word (the model held on it, having
// reported already) gets a notice of eagent's own, and the notice's look
// fails twice. afterFailure says down; turnFailed gives no notice, since
// "the session ends ... in its own words (maybeEnd, fallbackFinal)"; but
// maybeEnd's down case is skipped when the closing word was asked
// (finalAsked), so the session ends with exit 2 ("waiting for you") and
// the user is never told that eagent's model fails (DESIGN.md: "After the
// second failure the harness tells the user").
func TestSR7LoopAnEndingSessionDownAfterItsClosingWordNeverSaysItsModelFails(t *testing.T) {
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
	for _, m := range s.ui.said() {
		if strings.Contains(m, "kept failing") || strings.Contains(m, "keep failing") || strings.Contains(m, "can't reach") || strings.Contains(m, "turning my calls away") {
			return
		}
	}
	t.Fatalf("ended as down (exit 2) after two failed looks and never told the user its model fails; said %q", s.ui.said())
}
