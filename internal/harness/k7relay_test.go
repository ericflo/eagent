package harness

// The sixteenth review of the relay area, fourth pass (sr4): starts,
// restarts, switches and stops, sends and their outcomes, waiting model
// changes, live settings.

import (
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// sr4CommandSeat is pi's seat taking one of pi's own commands while pi is
// idle: the first send waits for the test (it is being typed), then, as
// Seat.Send does for an idle agent whatever the mode, it is typed into the
// prompt as a command and reported delivered; the seat's next look says pi
// moved (/new). pi stays idle, so no state event follows (setDialogState
// says nothing for an unchanged state).
type sr4CommandSeat struct {
	*fakeSeat
	entered chan string
	gate    chan struct{}
	once    sync.Once
}

func (s *sr4CommandSeat) Send(id, text, mode string) (_ string, err error) {
	first := false
	s.once.Do(func() { first = true })
	if !first {
		return s.fakeSeat.Send(id, text, mode)
	}
	s.entered <- text
	<-s.gate
	s.fakeSeat.mu.Lock()
	s.fakeSeat.sent = append(s.fakeSeat.sent, text)
	s.fakeSeat.modes = append(s.fakeSeat.modes, mode)
	s.fakeSeat.mu.Unlock()
	s.emit(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
	s.emit(relay.Event{Kind: relay.KindInner, InnerID: "sess-new"})
	return "typed the command into pi", nil
}

// Stop session (app or web) in the loop step after the agent's seat has
// read a send's arrival in the transcript: the arrival is in the seat's
// events, waiting for the loop. stopSession kills the seat and settles its
// sends at once as unconfirmed; the arrival, posted after, is the stopped
// seat's word on its own send and is recorded as the newer one.
func TestSR4StopSessionSettlesBeforeTheArrivalTheSeatAlreadyRead(t *testing.T) {
	seat := newK1QuietSeat()
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	id := k1Typed(t, rt, seat)
	rt.sync(func() {
		// The seat's look read the arrival just as Stop session came in.
		seat.emit(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
		rt.stopSession("the user stopped the session")
	})
	waitFor(t, "the arrival the stopped seat read is the send's outcome", func() bool {
		var outcome state.SendOutcome
		rt.sync(func() { outcome = rt.st.SendOutcome(id) })
		return outcome == state.SendArrived
	})
}

// pi is idle. pi's own /new is sent with delivery after_turn (the phone's
// delivery choice, eagent.delivery, or relay_send's mode), and while it is
// being typed the user picks another effort in the thread's settings. pi
// is idle, so the seat types /new straight into its prompt as a command;
// no turn follows. The change waits for the command and is applied once it
// is recorded typed and the grace after it is over (zero in tests): it
// does not wait for a turn that will not come.
func TestAChangeWaitingOnAnAfterTurnCommandTypedIntoAnIdleAgentIsApplied(t *testing.T) {
	seat := &sr4CommandSeat{fakeSeat: newFakeSeat("…"), entered: make(chan string, 4), gate: make(chan struct{})}
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	_ = g
	rt := sr3PiRuntime(t)
	sent := make(chan string, 1)
	go func() {
		out, _ := rt.relayTool("relay_send", argStr(map[string]any{"text": "/new", "mode": "after_turn"}), argNum(nil), argList(nil), false)
		sent <- out
	}()
	recv(t, seat.entered, "/new being typed")
	var msg string
	var err error
	rt.sync(func() { msg, err = rt.repickRelay("pi", relayPick{"", "high"}) })
	if err != nil {
		t.Fatalf("repick: %v", err)
	}
	t.Logf("change: %q", msg)
	close(seat.gate)
	t.Logf("send: %q", recv(t, sent, "relay_send"))
	var id string
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelaySendData
			if ev.Type == event.RelaySend && ev.Decode(&d) == nil && d.Text == "/new" {
				id = d.ID
			}
		}
	})
	waitFor(t, "the command's delivered on the record", func() bool {
		done := false
		rt.sync(func() { done = rt.st.SendOutcome(id) == state.SendArrived })
		return done
	})
	// The command's delivered ends the wait (the grace is zero here): a
	// restart is under way once it is taken.
	var launching, waiting bool
	var st string
	var awaited int
	look := func() bool {
		rt.sync(func() {
			launching = rt.relays.launch("pi") != nil
			_, waiting = rt.waitingPick("pi")
			st = rt.st.Relays["pi"].State
			awaited = rt.st.Relays["pi"].Awaited()
		})
		return launching || !waiting
	}
	k2Until(5*time.Second, look)
	t.Logf("after the delivered: state %s, awaited %d, launching %v, change waiting %v", st, awaited, launching, waiting)
	if !launching && waiting {
		t.Errorf("the command was delivered and pi is idle with nothing on its way, but the change still waits (%q) and nothing will apply it: no turn follows a typed /new", msg)
	}
}
