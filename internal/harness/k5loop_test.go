package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// The sibling of f078318 that it left: a session with no terminal is
// ending, a notice of eagent's own asks for a look, and the provider
// refuses it as too long. noRestartNow stopped the restart, so the refusal
// was an ordinary failure; the retried look's first call came back and its
// next was refused too, and the second failure told the user (important)
// that the model refuses the conversation "even after I restarted it from
// my notes". No restart was made. Now a refusal as too long is final for
// an ending session: the look is not made again, nothing is said of it,
// and the session ends as done, in its closing path's words.
func TestSR3LoopAnsweredRefusalInAnEndingSessionSaysItRestarted(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var passes, after atomic.Int32
	var rt atomic.Pointer[Runtime]
	var injected, stopped atomic.Bool
	r0, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			passes.Add(1)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case injected.Load():
			if after.Add(1) == 2 {
				return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
			}
			return sr2TooLong
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
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("after %d injected %v\nsaid %q\nlogged %q", after.Load(), injected.Load(), ui.said(), ui.logged())
		}
	})
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatalf("the ending session did not end after the refusal (%d calls after the notice)", after.Load())
	}
	if n := passes.Load(); n != 0 {
		t.Fatalf("setup: %d dossier passes", n)
	}
	if !injected.Load() || after.Load() == 0 {
		t.Fatalf("setup: notice %v, %d calls after it", injected.Load(), after.Load())
	}
	for _, m := range ui.said() {
		if strings.Contains(m, "even after I restarted it from my notes") || strings.Contains(m, "I'll answer it") {
			t.Fatalf("told %q, and the session was ending (no restart, no answer to come)", m)
		}
	}
	if n := after.Load(); n != 1 {
		t.Fatalf("%d calls after the notice, want the one refused: a refusal is final for an ending session", n)
	}
	if code != 0 {
		t.Fatalf("exit %d, want 0: its closing word was said, and what was refused came after it", code)
	}
}

// The same ending session, where each retried look's first call comes
// back (a lookup) and the call after it is refused as too long: every
// failure is "answered", so the session is never down and never ends, and
// noRestartNow never lets the restart that would make it fit run. It
// pays for a call on each retry for as long as the process lives
// (errorRetryDelays: hourly in production; shortened here).
func TestSR3LoopAnEndingSessionRefusedAfterALookupNeverEnds(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Millisecond})
	var passes, after atomic.Int32
	var rt atomic.Pointer[Runtime]
	var injected, stopped atomic.Bool
	r0, _, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			passes.Add(1)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case injected.Load():
			after.Add(1)
			if m[len(m)-1]["role"] == "tool" {
				return sr2TooLong
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
	r6Until(t, "forty calls after the notice", func() bool {
		select {
		case code := <-done:
			done <- code
			return true
		default:
		}
		return after.Load() >= 40
	})
	select {
	case code := <-done:
		done <- code
		t.Logf("the session ended (exit %d) after %d calls", code, after.Load())
		return
	default:
	}
	if passes.Load() == 0 {
		t.Fatalf("an ending session made %d calls after the notice, %d of them refused as too long, made no restart, and still runs", after.Load(), after.Load()/2)
	}
}
