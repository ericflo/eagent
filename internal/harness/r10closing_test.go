package harness

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// The closing word's turn reads something, and its next call fails: eagent
// closes in its own words, once. The turn is not tried again (the fix for
// finding 1 of the tenth review owes the call of a failed turn; a closing
// turn that has its fallback sent owes none), so no second closing message
// follows the fallback.
func TestR10AClosingTurnThatFailsAfterAReadClosesOnce(t *testing.T) {
	var phase atomic.Int32
	_, ui, _ := batch(t, brief+" Stop when done.", "Done: #42 and #44 are merged.", func(_ string, msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		switch {
		case strings.Contains(tail, "finished a turn") && last != "tool" && phase.Load() == 0:
			phase.Store(1)
			return stopsWhenDone()
		case strings.Contains(allText(msgs), "The session is ending now") && last != "tool" && phase.Load() == 1:
			phase.Store(2)
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"merged"}`)}}
		case last == "tool" && phase.Load() == 2:
			phase.Store(3)
			return reply{status: 400, body: `{"error":{"message":"bad request","type":"invalid_request_error"}}`}
		case phase.Load() >= 3:
			return say("Closing report: #42 and #44 are merged.")
		}
		return hold()
	})
	said := ui.said()
	if n := strings.Count(strings.Join(said, "\n"), "I could not write a proper closing report"); n != 1 || len(said) != 1 {
		t.Fatalf("closing messages %q, want the fallback once and nothing after it", said)
	}
}
