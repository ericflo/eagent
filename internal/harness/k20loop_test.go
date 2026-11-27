package harness

import (
	"strings"
	"sync/atomic"
	"testing"
)

// The user writes while the first call of the closing turn is out, and that
// call answers in plain text (a closing report written without seeing their
// message). The session is not ending any more (stillClosing), as in
// TestR12MessageDuringAHoldingClosingCallGetsNoClosingWords: plain text is
// then a reply like any other, said to no one, and the user gets one answer.
// Here the plain text is taken for the closing report and sent, as an
// important message, to a session that is not ending.
func TestSR12PlainTextClosingReportAfterTheUserWroteIsNotSent(t *testing.T) {
	const report = "Final report: #42 is merged and this session is ending."
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			r12Write(rt, "is #42 merged?")
			return reply{text: report}
		case phase.CompareAndSwap(2, 3):
			return say("Yes, #42 is merged and the tests are green.")
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	code := coreExit(s)
	if phase.Load() < 2 {
		t.Fatalf("setup: no closing call (phase %d)", phase.Load())
	}
	for _, m := range s.ui.said() {
		if strings.Contains(m, report) {
			t.Fatalf("exit %d: the closing report written before the user's message was sent after the session stopped ending; said %q", code, s.ui.said())
		}
	}
}
