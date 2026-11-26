package relay

import (
	"errors"
	"slices"
	"strings"
	"sync"
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

// closingHost closes the seat the first time the send takes its last look
// at the screen before typing: a stop that lands after the seat said it was
// ready and before the text is pasted.
type closingHost struct {
	*fakeHost
	once sync.Once
	seat *Seat
}

func (h *closingHost) Capture(target string, lines int) (string, error) {
	h.once.Do(h.seat.Close)
	return h.fakeHost.Capture(target, lines)
}

// Finding 12: a seat closed after it was found ready, before the text is
// typed, types nothing and says so with ErrClosed (not_sent), not as a
// send that may have gone.
func TestR7ASeatClosedJustBeforeTypingTypesNothing(t *testing.T) {
	t.Parallel()
	fh := &fakeHost{alive: true}
	h := &closingHost{fakeHost: fh}
	s := newSeat(Spec{}, claudeAgent{}, h, "x", "")
	h.seat = s
	s.state = "idle"
	close(s.done)
	_, err := s.Send("r1", "count the chairs", "queue")
	if !errors.Is(err, ErrClosed) {
		t.Errorf("Send returned %v, want ErrClosed", err)
	}
	fh.mu.Lock()
	defer fh.mu.Unlock()
	if len(fh.typed) != 0 || len(fh.keys) != 0 {
		t.Errorf("typed %q, pressed %q into a closed seat", fh.typed, fh.keys)
	}
}
