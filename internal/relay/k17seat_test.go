package relay

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func k17Prompt(text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":"%s","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, stamp(time.Now()), text)
}

// k17Seat is a working Claude seat with r1 typed and tracked, and feed,
// which folds transcript lines into it.
func k17Seat(t *testing.T, text string) (*Seat, *fakeHost, func(...string)) {
	t.Helper()
	h := &fakeHost{alive: true}
	s := newSeat(Spec{}, claudeAgent{}, h, "k17", "")
	s.state = "working"
	s.track("r1", text, "queue")
	p := &claudeParser{}
	return s, h, func(lines ...string) {
		for _, l := range lines {
			for _, o := range p.line([]byte(l)) {
				s.fold(o)
			}
		}
	}
}

func k17Arrivals(s *Seat, id string) (ok, failed int) {
	for _, ev := range drain(s.Events()) {
		if ev.Kind == KindDelivered && ev.SendID == id {
			if ev.OK {
				ok++
			} else {
				failed++
			}
		}
	}
	return ok, failed
}

func k17Watched(s *Seat) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for _, p := range s.pending {
		ids = append(ids, p.id)
	}
	return ids
}

// A message typed while Claude works arrives at its enqueue line. An
// interrupt (relay_control's Escape) then empties Claude's queue back into
// the input box (popAll), unread. The send is watched again: once Claude
// is idle and its words sit in the box the seat presses Enter, as for any
// send left there, and the prompt that follows confirms it. (It stayed
// recorded as arrived while it sat in the box for good; main the same.)
func TestK17AQueuedSendPutBackInTheBoxByAnInterruptIsSubmitted(t *testing.T) {
	const text = "also update the changelog"
	s, h, feed := k17Seat(t, text)
	feed(k1Queue("enqueue", text))
	if ok, _ := k17Arrivals(s, "r1"); ok != 1 || len(k17Watched(s)) != 0 {
		t.Fatalf("after the enqueue line: %d arrivals, watched %v; want it arrived and not watched", ok, k17Watched(s))
	}
	feed(k1Queue("popAll", ""), k17Prompt("[Request interrupted by user]"))
	if got := k17Watched(s); !slices.Equal(got, []string{"r1"}) {
		t.Fatalf("after the interrupt put it back into the input box: watched %v; want r1", got)
	}
	h.mu.Lock()
	h.screen = "● Interrupted\n\n╭──────────────────────────────╮\n│ > " + text + "    │\n╰──────────────────────────────╯\n  ? for shortcuts"
	h.mu.Unlock()
	now := time.Now()
	s.mu.Lock()
	state := s.state
	s.stateAt = now.Add(-time.Minute)
	s.mu.Unlock()
	if state != "idle" {
		t.Fatalf("state %q after the interrupt; want idle", state)
	}
	for _, after := range []time.Duration{1, 4, 7, 10} {
		s.checkScreen(now.Add(after * time.Second))
	}
	h.mu.Lock()
	keys := append([]string(nil), h.keys...)
	h.mu.Unlock()
	if !slices.Contains(keys, "Enter") {
		t.Fatalf("keys %q; want Enter pressed on the message left in the box", keys)
	}
	feed(k17Prompt(text))
	if ok, failed := k17Arrivals(s, "r1"); ok != 1 || failed != 0 || len(k17Watched(s)) != 0 {
		t.Fatalf("after the prompt showed it: %d arrivals, %d failures, watched %v; want it confirmed", ok, failed, k17Watched(s))
	}
}

// A queued message Claude takes at the end of its turn is shown by the
// prompt after its dequeue line. It is not watched again by a later
// interrupt that finds the queue empty, nor by one that puts another
// message of the same words back (the user's own, typed in the terminal
// after it).
func TestK17AQueuedSendAPromptShowedIsNotWatchedAgain(t *testing.T) {
	const text = "also update the changelog"
	s, _, feed := k17Seat(t, text)
	feed(k1Queue("enqueue", text), k1Queue("dequeue", ""), k17Prompt(text))
	if ok, _ := k17Arrivals(s, "r1"); ok != 1 {
		t.Fatalf("arrivals %d; want one, at the enqueue line", ok)
	}
	feed(k1Queue("popAll", ""))
	feed(k1Queue("enqueue", text), k1Queue("popAll", ""))
	if ok, failed := k17Arrivals(s, "r1"); ok != 0 || failed != 0 || len(k17Watched(s)) != 0 {
		t.Fatalf("after a prompt showed it: %d more arrivals, %d failures, watched %v; want nothing more of it", ok, failed, k17Watched(s))
	}
}
