package relay

import (
	"slices"
	"strings"
	"testing"
)

// Finding 7: a long send the transcript shows cut short keeps its entry
// against a short waiting send whose words sit inside it ("yes" inside
// "yesterday"); the short one has not arrived.
func TestR7ACutShortLongSendIsNotTakenByAShortSendInsideIt(t *testing.T) {
	t.Parallel()
	long := "Please check yesterday's deployment log for the payment service " + strings.Repeat("and list every error it shows with the time it happened ", 4)
	if len(long) <= 200 {
		t.Fatalf("long is %d bytes, want over 200", len(long))
	}
	for _, entry := range []string{long[:100], long[:200] + "…"} {
		s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
		s.track("r1", "yes", "queue")
		s.track("r2", long, "queue")
		if got := s.matchPending(entry); !slices.Equal(got, []string{"r2"}) {
			t.Errorf("the entry %q confirmed %v, want r2", entry, got)
		}
	}
}
