package harness

import (
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/store"
)

func r13nWaitingSeat(reply string) *r12dWaitingSeat {
	return &r12dWaitingSeat{fakeSeat: newFakeSeat(reply), entered: make(chan string, 4), closed: make(chan struct{})}
}

func r13nEntered(t *testing.T, s *r12dWaitingSeat, what string) string {
	t.Helper()
	select {
	case id := <-s.entered:
		return id
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was never begun", what)
	}
	return ""
}

// Finding 14, a switch's brief superseded. A switch (codex -> claude) is
// typing its handoff brief when a second switch (claude -> codex) takes
// over; the second agent is typing its own brief when the session quits.
// The first brief was never typed: it is not_sent on the record, once,
// before session.end.
func TestR13NASupersededSwitchBriefIsNotSentAtAQuit(t *testing.T) {
	two, three := r13nWaitingSeat("two"), r13nWaitingSeat("three")
	rt, g, done := r10Session(t, &lingerSeat{newFakeSeat("one")}, two, three)
	sw1 := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	brief := r13nEntered(t, two, "the first switch's brief")
	sw2 := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "codex", "relay": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	r13nEntered(t, three, "the second switch's brief")
	evs := r10Quit(t, rt, g, 0, done)
	recv(t, sw1, "the first switch")
	recv(t, sw2, "the second switch")
	r10NotSentBeforeEnd(t, evs, brief)
}

// r13nLateSeat is an agent that came up after the session was stopped:
// finishLaunch kills it at once. Killing it lets the session's end go on
// (done), with work queued on the loop ahead of anything handed to it
// afterwards, so the loop closes before it reaches that.
type r13nLateSeat struct {
	*fakeSeat
	rt   *Runtime
	done func()
	once sync.Once
}

func (s *r13nLateSeat) Kill() error {
	s.once.Do(func() {
		for range 512 {
			select {
			case s.rt.loop <- func() {}:
			default:
			}
		}
		s.done()
	})
	return s.fakeSeat.Kill()
}

// Finding 14, a restart under way when the session is stopped. A send is
// waiting for the agent when a model change restarts it; the session is
// stopped while the new agent starts (a model call still finishing keeps
// the loop's end draining), and the restart is recorded as stopped. The
// send was never typed: it is not_sent on the record, once, before
// session.end.
func TestR13NASendHeldForARestartStoppedWithTheSessionIsNotSent(t *testing.T) {
	one := r13nWaitingSeat("one")
	late := &r13nLateSeat{fakeSeat: newFakeSeat("two")}
	rt, g, done := r10Session(t, one, late)
	late.rt = rt
	sent := r10Send(rt, "check the version")
	id := r13nEntered(t, one, "the send")
	rt.sync(func() {
		// The restart a model change makes (a message on its way now holds
		// a change asked back, item 3; this test is about the restart).
		if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	rt.wg.Add(1) // the model call still finishing
	late.done = rt.wg.Done
	rt.sync(func() { rt.stopSession("stopped") })
	g.release <- struct{}{}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	recv(t, sent, "relay_send")
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	r10NotSentBeforeEnd(t, evs, id)
}
