package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// The ninth review's suspicion: relay_send stops waiting for its send's
// typing because the session ends (awaitSend at r.closed), and the typing
// then succeeds. The tool said "Could not send to Codex: the session is
// ending", which a resumed model reads as not sent and may send again. It
// says what is true: the session ended before the send was confirmed, and
// the send's own outcome decides.
func TestK12ASendTheSessionEndedOnSaysSo(t *testing.T) {
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	out := make(chan string, 1)
	go func() {
		text, _ := rt.relayTool("relay_send", argStr(map[string]any{"text": "run the tests"}), argNum(nil), argList(nil), false)
		out <- text
	}()
	id := <-seat.entered
	close(rt.closed) // the session's end: the loop has stopped
	text := recv(t, out, "relay_send")
	if strings.Contains(text, "Could not send") || !strings.Contains(text, "ended before") {
		t.Errorf("relay_send as the session ended: %q; want that the session ended before the send was confirmed, not that it could not send", text)
	}
	seat.result <- nil // the typing goes through
	k1QueuesIdle(t, rt)
	for _, d := range deliveredFor(rt, id) {
		if d.Outcome == event.DeliveryNotSent {
			t.Errorf("%s, typed: recorded not sent (%q)", id, d.Detail)
		}
	}
}

// The same for a switch's brief: the switch stops waiting for the brief's
// typing as the session ends; it does not record the brief not sent, nor
// say it could not be typed: the brief's own outcome decides.
func TestK12ASwitchTheSessionEndedOnSaysSo(t *testing.T) {
	next := &heldSendSeat{fakeSeat: newFakeSeat("two"), entered: make(chan string, 4), result: make(chan error, 4)}
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), next}, nil)
	rt := newLaunchRuntime(t)
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	var id string
	select {
	case id = <-next.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the brief was never typed")
	}
	close(rt.closed)
	text := recv(t, switched, "switch")
	if strings.Contains(text, "could not be typed") || !strings.Contains(text, "ended before") {
		t.Errorf("switch as the session ended: %q; want that the session ended before the brief was confirmed", text)
	}
	next.result <- nil
	// Its typing ends (the barrier after it stays queued; once the loop
	// has closed nothing more is taken, and finish records what is left).
	waitFor(t, "the brief's typing ended", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		q := rt.relays.queues[next]
		return q == nil || q.typing == ""
	})
	for _, d := range deliveredFor(rt, id) {
		if d.Outcome == event.DeliveryNotSent {
			t.Errorf("the brief %s, typed: recorded not sent (%q)", id, d.Detail)
		}
	}
}

// The ninth review: the user quits while a turn's relay_send waits for its
// send to be typed (seat.Send waits for the agent's prompt, up to 40 s);
// the drain's grace runs out. Its log said "a model call did not stop in
// time"; it says what the turn waited on: the send being typed.
func TestK12ADrainSaysItWaitedOnASendBeingTyped(t *testing.T) {
	saved := turnStopGrace
	turnStopGrace = 20 * time.Millisecond
	t.Cleanup(func() { turnStopGrace = saved })
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	rt, _, done := r10Session(t, seat)
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		_, _ = rt.relayTool("relay_send", argStr(map[string]any{"text": "run the tests"}), argNum(nil), argList(nil), false)
	}()
	id := <-seat.entered
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.closed: // past the grace
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never closed")
	}
	seat.result <- nil // the typing ends (seat.Send is bounded: 40 s)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	logged := strings.Join(rt.ui.(*fakeUI).logged(), "\n")
	if strings.Contains(logged, "a model call did not stop in time") || !strings.Contains(logged, "being typed into Codex ("+id+")") {
		t.Errorf("the drain's log: %q; want that the turn waited on the send %s being typed into Codex", logged, id)
	}
}
