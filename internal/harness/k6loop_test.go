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

// The closing word of a batch session is refused as too long, which is
// final for a session that is ending, and the user writes during its last
// title and summary (refused too): the session stops ending, and the look
// at their message restarts the conversation first. The restart records
// the size that set it off as the ordinary refusal raises it (the window's
// restart size at least), not the last prompt that came back.
func TestK6ARestartAfterARefusalWhileEndingRecordsTheRefusedSize(t *testing.T) {
	coreSummariesOn(t, false)
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var phase, passes atomic.Int32
	var rt atomic.Pointer[Runtime]
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			passes.Add(1)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		summary := strings.Contains(tail, "this look is only about the thread's title and summary")
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			return sr2TooLong
		case summary && phase.CompareAndSwap(2, 3):
			r12Write(&rt, "did coat 2 dry?")
			return sr2TooLong
		case last != "tool" && !summary && phase.CompareAndSwap(3, 4):
			return say("Yes, coat 2 is dry.")
		case last == "tool":
			return hold()
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsAndReports("Done: both coats are on.")
		}
		return hold()
	})
	cfg := testConfig(model.srv.URL)
	cfg.RolloverTokens = 150_000
	s := audit2Start(t, cfg, Options{Prompt: brief}, newFakeSeat("Both coats are on."), model, func(r *Runtime) { rt.Store(r) })
	code := coreExit(s)
	if phase.Load() < 4 || passes.Load() == 0 {
		t.Fatalf("setup: phase %d, %d dossier passes, exit %d, logged %q", phase.Load(), passes.Load(), code, s.ui.logged())
	}
	const want = 150_000
	var got []int
	for _, ev := range mustReadCore(t, s.rt.sess.Path) {
		var d event.SubsessionEndData
		if ev.Type == event.SubsessionEnd && ev.Decode(&d) == nil {
			got = append(got, d.InputTokens)
		}
	}
	if len(got) != 1 || provTokens(got[0]) < want {
		t.Fatalf("the restart recorded input_tokens %v, want one at least %d (the size a refusal as too long implies)", got, want)
	}
}
