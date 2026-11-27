package harness

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// The last title and summary of a batch session reads something, and the
// user writes while that call is out. The turn ends "not closing"; the
// title and summary it was asked for must still be asked for (at 87ebbd0
// the call after the read carried the thread request with the user's
// message), so the session does not end with them never written.
func TestRV12LastSummaryInterruptedByUserStillGetsWritten(t *testing.T) {
	coreSummariesOn(t, false)
	var phase atomic.Int32
	var threadAskedAfterRead atomic.Int32
	s, model := r12Start(t, newFakeSeat("Both coats are on."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		summary := strings.Contains(tail, "this look is only about the thread's title and summary")
		threadAsk := strings.Contains(tail, "The thread's title and summary may be out of date")
		switch {
		case strings.Contains(tail, "The session is ending now"):
			if threadAsk {
				return reply{calls: []event.ToolCall{tc("update_thread", `{"title":"Paint both coats","summary":"Both coats are on; coat 2 is dry."}`)}}
			}
			return hold()
		case last == "tool":
			return hold()
		case summary && phase.CompareAndSwap(1, 2):
			r12Write(rt, "did coat 2 dry?")
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"coats"}`)}}
		case phase.Load() >= 2 && strings.Contains(allText(msgs), "did coat 2 dry?"):
			if threadAsk {
				threadAskedAfterRead.Add(1)
				return reply{calls: []event.ToolCall{
					tc("send_message", `{"text":"Yes, coat 2 is dry."}`),
					tc("update_thread", `{"title":"Paint both coats","summary":"Both coats are on; coat 2 is dry."}`),
				}}
			}
			if phase.CompareAndSwap(2, 3) {
				return say("Yes, coat 2 is dry.")
			}
			return hold()
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsAndReports("Done: both coats are on.")
		}
		return hold()
	})
	code := coreExit(s)
	if phase.Load() < 2 {
		t.Fatalf("the last summary never read anything with the user writing (phase %d)", phase.Load())
	}
	for i, msgs := range audit2Requests(model) {
		t.Logf("request %d (last role %v): %q", i, msgs[len(msgs)-1]["role"], clipRunes(lastText(msgs), 400))
		// No notice was recorded: the only thing after the last look is
		// the user's message and the "not closing" turn end.
		if strings.Contains(lastText(msgs), "did coat 2 dry?") || strings.Contains(lastText(msgs), "The user wrote") {
			if strings.Contains(lastText(msgs), "A notice for you is above") {
				t.Errorf("request %d tells the model a notice is above, and none was recorded: %q", i, lastText(msgs))
			}
		}
	}
	titles := coreTitles(mustReadCore(t, s.rt.sess.Path))
	t.Logf("exit %d, said %q, titles %v, not-closing ends %d, thread asked after read %d", code, s.ui.said(), titles, len(r12Ends(t, s, "not closing")), threadAskedAfterRead.Load())
	if len(titles) == 0 {
		t.Fatalf("the session ended (exit %d) with the last title and summary never written: no call after the read was asked for them (turn ends \"not closing\": %d)", code, len(r12Ends(t, s, "not closing")))
	}
}
