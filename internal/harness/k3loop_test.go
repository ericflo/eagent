package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// SR1: a session with no terminal stops its agent; the call after that
// fails, and so does the retry's first call: two failures in a row, the
// second with no call of its turn coming back. The user is told "I keep
// trying and will pick up where I left off", and in the same moment the
// session ends as one whose model is down (maybeEnd's down case): nothing
// keeps trying. What the user is told must not promise a retry the
// session is about to drop.
func TestSRLoopDownNoticePromisesRetriesTheEndingSessionNeverMakes(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var calls atomic.Int32
	_, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if calls.Add(1) == 1 {
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"the work is done"}`)}}
		}
		return k1BadRequest
	})
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	var promise, closing string
	for _, m := range ui.said() {
		if strings.Contains(m, "I keep trying") {
			promise = m
		}
		if strings.HasPrefix(m, "This session is ending") {
			closing = m
		}
	}
	if promise != "" && closing != "" {
		t.Fatalf("exit %d: told %q, then the session ended at once: %q", code, promise, closing)
	}
}
