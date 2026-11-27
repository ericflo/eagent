package harness

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// The closing turn sends its report with send_message and looks something
// up in the same reply, so it gets another call. That call answers in plain
// text, with no tool call. The report was already sent (tc.spoke): plain
// text is the report only when none was sent. Here it goes out as a second
// important message, the closing report twice.
func TestSR11ClosingPlainTextAfterReportIsNotSentAgain(t *testing.T) {
	const report = "Final: both coats are on and dry."
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Both coats are on."), func(_ *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		switch {
		case last == "tool" && phase.CompareAndSwap(2, 3):
			// The call after the lookup: the model restates its report as
			// plain text, as models often do.
			return reply{text: report}
		case last == "tool":
			return hold()
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			return reply{calls: []event.ToolCall{
				tc("send_message", `{"text":"`+report+`"}`),
				tc("session_search", `{"query":"coats"}`),
			}}
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	code := coreExit(s)
	n := 0
	for _, m := range s.ui.said() {
		if strings.Contains(m, report) {
			n++
		}
	}
	t.Logf("exit %d, phase %d, said %q", code, phase.Load(), s.ui.said())
	if phase.Load() < 3 {
		t.Fatalf("the closing turn never made its call after the lookup (phase %d)", phase.Load())
	}
	if n != 1 {
		t.Fatalf("the closing report reached the user %d times, want once: %q", n, s.ui.said())
	}
}
