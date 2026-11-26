package harness

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// The last title and summary of a batch session that is ending (inline)
// reads something, and the call after the read fails, as does every call
// after it. That call is one attempt, as the dedicated summarizer's is: it
// is not owed again, so the user is not told after the closing report that
// eagent cannot reach its model, and the session ends done.
func TestR11FinalSummaryThatFailsAfterAReadEndsDone(t *testing.T) {
	coreSummariesOn(t, false)
	var phase, after atomic.Int32
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		switch {
		case phase.Load() >= 2:
			after.Add(1)
			return r10Bad
		case last == "tool" && phase.CompareAndSwap(1, 2):
			return r10Bad
		case last == "tool":
			return hold()
		case strings.Contains(tail, "this look is only about the thread's title and summary") && phase.CompareAndSwap(0, 1):
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"coats"}`)}}
		case strings.Contains(tail, "finished a turn"):
			return stopsAndReports("Done: both coats are on.")
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief + " Stop when done."}, newFakeSeat("Both coats are on."), model, nil)
	code := coreExit(s)
	said := s.ui.said()
	if phase.Load() < 2 {
		t.Fatalf("the last title and summary call never failed after its read (phase %d); said %q", phase.Load(), said)
	}
	if code != 0 || len(said) != 1 {
		t.Fatalf("exit %d, said %q, %d calls after the failure; want exit 0 and the closing report alone", code, said, after.Load())
	}
}

// The closing turn of a session whose user has heard nothing starts a new
// agent, and the call after the start fails. The session is not ending any
// more: the failed call is owed as any turn's is, and made again (it gives
// the new agent its work), and nothing says the session is ending.
func TestR11ClosingTurnThatStartsAnAgentIsTriedAgain(t *testing.T) {
	first, second := newFakeSeat("First coat on."), newFakeSeat("Second coat on.")
	useSeatList(t, first, second)
	var phase atomic.Int32
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		switch {
		case phase.CompareAndSwap(2, 3):
			// The call after the start (the start shows after its result).
			return r10Bad
		case last == "tool":
			return hold()
		case phase.CompareAndSwap(3, 4):
			return reply{calls: []event.ToolCall{tc("relay_send", `{"text":"paint the second coat"}`)}}
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			return reply{calls: []event.ToolCall{tc("relay_start", `{"profile":"codex"}`)}}
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(4, 5):
			return stopsAndReports("Done: both coats are on.")
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, nil, model, nil)
	sent := func() bool { return slices.Contains(second.sentTexts(), "paint the second coat") }
	s.waitFor("the failed call made again, or the closing turn ended owing nothing", func() bool {
		return sent() || len(r11Ends(s, "error, closing")) > 0
	})
	if !sent() {
		t.Fatalf("the closing turn ended %q, and the call that failed is never made again", r11Ends(s, "error, closing"))
	}
	if code := coreExit(s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if said := s.ui.said(); r5Told(s, "This session is ending") != 0 || len(said) != 1 {
		t.Fatalf("said %q; want the closing report alone", said)
	}
}

// The same with a timer: the closing turn sets one, and the call after it
// fails. The session stays to keep the timer, so eagent does not say it is
// ending with nothing running, and the failed call is made again.
func TestR11ClosingTurnThatSetsATimerIsTriedAgain(t *testing.T) {
	var phase atomic.Int32
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		switch {
		case last == "tool" && phase.CompareAndSwap(2, 3):
			return r10Bad
		case last == "tool":
			return hold()
		case phase.CompareAndSwap(3, 4):
			return say("I'll check that staging is still up in an hour.")
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			return reply{calls: []event.ToolCall{tc("schedule", `{"spec":"1h","note":"Check that staging is still up."}`)}}
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, newFakeSeat("Deployed to staging."), model, nil)
	s.waitFor("the failed call made again, or eagent's closing words", func() bool {
		return r5Told(s, "I'll check") > 0 || r5Told(s, "This session is ending") > 0
	})
	if n := r5Told(s, "This session is ending"); n != 0 {
		t.Fatalf("said %q with a timer set", s.ui.said())
	}
	s.settled()
	if said := s.ui.said(); len(said) != 1 {
		t.Fatalf("said %q; want the answer of the call made again alone", said)
	}
}

// r11Ends lists eagent's turn ends recorded with the given reason.
func r11Ends(s *session, reason string) []string {
	var out []string
	for _, ev := range s.events(event.TurnEnd) {
		var d event.TurnData
		if ev.Actor == event.ActorEagent && ev.Decode(&d) == nil && d.Reason == reason {
			out = append(out, d.Reason)
		}
	}
	return out
}
