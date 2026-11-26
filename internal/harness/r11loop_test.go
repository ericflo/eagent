package harness

import (
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
