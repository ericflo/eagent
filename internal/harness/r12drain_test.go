package harness

import (
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// r12dWaitingSeat is an agent a send waits on (for it to be ready, say):
// once the agent is stopped (Kill) or its terminal closed, the send
// returns relay.ErrClosed with nothing typed, as relay.Seat's does.
type r12dWaitingSeat struct {
	*fakeSeat
	entered chan string
	closed  chan struct{}
	once    sync.Once
}

func (s *r12dWaitingSeat) Send(id, text, mode string) (_ string, err error) {
	defer s.sendStart(id)(&err)
	s.entered <- id
	<-s.closed
	return "", relay.ErrClosed
}

func (s *r12dWaitingSeat) Kill() error {
	s.once.Do(func() { close(s.closed) })
	return s.fakeSeat.Kill()
}

func (s *r12dWaitingSeat) Close() {
	s.once.Do(func() { close(s.closed) })
	s.fakeSeat.Close()
}

// Finding 6. A send waits for the agent when the session is stopped
// (stopSession: the agent is killed before the loop closes, so the send
// ends not sent while the loop is still open). The loop has work queued
// ahead of the send's outcome and closes before it reaches it: the send
// was never typed, and the record says not_sent, once, before session.end.
func TestR12DASendEndedNotSentAsTheLoopClosesIsNotSent(t *testing.T) {
	seat := &r12dWaitingSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), closed: make(chan struct{})}
	rt, _, done := r10Session(t, seat)
	r10Send(rt, "check the version")
	var id string
	select {
	case id = <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the send was never begun")
	}
	queued, gate := make(chan int, 1), make(chan struct{})
	rt.post(func() {
		// Work ahead of the send's outcome on the loop.
		for range 512 {
			select {
			case rt.loop <- func() {}:
			default:
			}
		}
		queued <- len(rt.loop)
		rt.stopSession("stopped")
		<-gate
	})
	ahead := <-queued
	waitFor(t, "the send's outcome handed to the loop", func() bool { return len(rt.loop) > ahead })
	close(gate)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	r10NotSentBeforeEnd(t, evs, id)
}
