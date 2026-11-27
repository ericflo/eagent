package relay

import (
	"errors"
	"slices"
	"testing"
	"time"
)

// rv14QueueTakesThenFails is Codex whose own queue took the message (the
// transcript shows it arriving while `codex queue` is still returning) and
// whose `codex queue` then reports a failure (killed at its bound, exit 1).
type rv14QueueTakesThenFails struct{ codexAgent }

func (rv14QueueTakesThenFails) afterTurn(s *Seat, _, text string) error {
	s.fold(obs{kind: "user", text: text})
	return errors.New("codex queue: signal: killed")
}

// rv14QueueTakesAndWorks is Codex whose queue took the message, the
// transcript showing it arriving before `codex queue` returned 0.
type rv14QueueTakesAndWorks struct{ codexAgent }

func (rv14QueueTakesAndWorks) afterTurn(s *Seat, _, text string) error {
	s.fold(obs{kind: "user", text: text})
	return nil
}

// A hand-over whose message the transcript already shows arriving is not
// typed into the agent a second time when the hand-over then reports a
// failure. At ecf0cf8 the arrival confirmed it and Send returned "held";
// now it is typed again.
func TestR14RVHandOverThatArrivedThenFailedIsNotTypedAgain(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	const text = "then commit the fix"
	h := &fakeHost{alive: true}
	s := newSeat(Spec{}, rv14QueueTakesThenFails{}, h, "rv14c-codex-fails", "thread-1")
	s.state = "working"
	if _, err := s.Send("r1", text, "after_turn"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	typed := slices.Clone(h.typed)
	h.mu.Unlock()
	ok := okDeliveries(drain(s.events))
	if len(typed) != 0 {
		t.Fatalf("typed %q after the transcript showed it arriving from Codex's queue (confirmed %v): the message goes in twice", typed, ok)
	}
}

// A send whose arrival the transcript shows while its hand-over returns is
// settled (confirmed, or at least reported), never left pending for ever.
// At ecf0cf8 the arrival confirmed it.
func TestR14RVArrivalReadDuringHandOverIsNotPendingForEver(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	for _, c := range []struct {
		name string
		mk   func() (*Seat, Host)
	}{
		{"codex_queue", func() (*Seat, Host) {
			h := &fakeHost{alive: true}
			return newSeat(Spec{}, rv14QueueTakesAndWorks{}, h, "rv14c-codex-works", "thread-1"), h
		}},
		{"antigravity_native_queue", func() (*Seat, Host) {
			h := &arrivingHost{fakeHost: &fakeHost{alive: true}, text: "then commit the fix"}
			s := newSeat(Spec{}, antigravityAgent{}, h, "rv14c-agy", "")
			h.seat = s
			return s, h
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _ := c.mk()
			s.state = "working"
			if _, err := s.Send("r1", "then commit the fix", "after_turn"); err != nil {
				t.Fatal(err)
			}
			// The turn it started ends; the seat stays idle a long time and
			// looks at its screen.
			s.mu.Lock()
			s.state, s.stateAt = "idle", time.Now().Add(-time.Hour)
			s.mu.Unlock()
			later := time.Now().Add(time.Hour)
			if s.wantsDialogCheck(later) {
				s.checkScreen(later)
			}
			s.checkScreen(later)
			var got []string
			for _, ev := range deliveries(drain(s.events)) {
				got = append(got, ev.SendID)
			}
			if len(got) == 0 && len(s.Snapshot().Pending) != 0 {
				t.Fatalf("an hour after it arrived: no outcome, still pending %v", s.Snapshot().Pending)
			}
		})
	}
}
