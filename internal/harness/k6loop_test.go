package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// The shape of ce49418 with another refusal: a session with no terminal
// is ending, a notice of eagent's own asks for a look, and each retried
// look's first call comes back (a lookup) while the call after it is
// refused (a 400 that is not "too long"). Every failure is answered, so
// the session is never down; it retried on the delays for as long as the
// process lived. A refused call is final for an ending session: it ends,
// as done, in its closing path's own words.
func TestK6AnEndingSessionWhoseCallsAreRefusedEnds(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Millisecond})
	var after atomic.Int32
	var rt atomic.Pointer[Runtime]
	var injected, stopped atomic.Bool
	r0, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		switch {
		case injected.Load():
			after.Add(1)
			if m[len(m)-1]["role"] == "tool" {
				return k1BadRequest
			}
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
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
	rt.Store(r0)
	r6Until(t, "the session to end, or forty calls after the notice", func() bool {
		select {
		case code := <-done:
			done <- code
			return true
		default:
		}
		return after.Load() >= 40
	})
	var code int
	select {
	case code = <-done:
		done <- code
	default:
		t.Fatalf("an ending session made %d calls after the notice, half of them refused, and still runs", after.Load())
	}
	if n := after.Load(); n != 2 {
		t.Fatalf("%d calls after the notice, want the two of the one look", n)
	}
	for _, m := range ui.said() {
		if strings.Contains(m, "I keep trying") {
			t.Fatalf("told %q, and the session ended", m)
		}
	}
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
}
