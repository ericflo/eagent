package relay

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Finding 13: on a reattach, an entry Claude stamped to the millisecond
// before a restored send was typed is an earlier send of the same words,
// not this one's arrival; only a transcript that stamps to the whole
// second (Antigravity) gets the second's grace.
func TestR7AnEarlierSameSecondEntryDoesNotConfirmARestoredSend(t *testing.T) {
	t.Parallel()
	sec := time.Now().Add(-time.Minute).Truncate(time.Second)
	r := Restored{ID: "r2", Text: "continue", Mode: "queue", At: sec.Add(500 * time.Millisecond)}
	s := attachSeat(Spec{Restore: []Restored{r}}, claudeAgent{}, &fakeHost{alive: true}, "x", "", "", sec.Add(800*time.Millisecond))
	if ok := claudeLines(s, claudeUser(sec.Add(100*time.Millisecond), "continue")); len(ok) != 0 {
		t.Fatalf("an entry 400ms before r2 was typed confirmed %v", ok)
	}
}

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
