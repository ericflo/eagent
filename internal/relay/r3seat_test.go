package relay

import (
	"errors"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// A send waiting for an agent still on its startup screens stops waiting
// when the seat is closed or killed (a stop, a restart, a switch), with an
// error the harness can tell apart from "still starting".
func TestSendWaitingForAStartingAgentStopsWhenTheSeatCloses(t *testing.T) {
	for _, how := range []string{"close", "kill"} {
		t.Run(how, func(t *testing.T) {
			h := &fakeHost{alive: true}
			s := newSeat(Spec{Name: "codex", Profile: config.RelayProfile{Agent: "codex"}}, codexAgent{}, h, "eagent-1-codex", "")
			close(s.done) // no watcher runs in this test
			errc := make(chan error, 1)
			go func() {
				_, err := s.Send("r1", "hello", "queue")
				errc <- err
			}()
			time.Sleep(100 * time.Millisecond)
			if how == "close" {
				s.Close()
			} else {
				_ = s.Kill()
			}
			select {
			case err := <-errc:
				if !errors.Is(err, ErrClosed) {
					t.Fatalf("send ended with %v, want ErrClosed", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the send still waits for a seat that was closed")
			}
			if len(h.typed) != 0 {
				t.Fatalf("typed into a closed seat: %q", h.typed)
			}
		})
	}
}

// A send to a seat that was already closed is not typed, whatever state
// the seat last saw.
func TestSendToAClosedSeatIsNotTyped(t *testing.T) {
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "codex", Profile: config.RelayProfile{Agent: "codex"}}, codexAgent{}, h, "eagent-1-codex", "")
	s.state = "idle"
	close(s.done)
	s.Close()
	if _, err := s.Send("r1", "hello", "queue"); !errors.Is(err, ErrClosed) {
		t.Fatalf("send: %v, want ErrClosed", err)
	}
	if len(h.typed) != 0 {
		t.Fatalf("typed into a closed seat: %q", h.typed)
	}
}
