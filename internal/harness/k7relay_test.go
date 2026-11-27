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
	"github.com/ericflo/eagent/internal/store"
)

// sr4CommandSeat is pi's seat taking one of pi's own commands while pi is
// idle: the first send waits for the test (it is being typed), then, as
// Seat.Send does for an idle agent whatever the mode, it is typed into the
// prompt as a command; the seat's next look says pi moved (/new) and then
// that the command was delivered (releaseHeld). pi stays idle, so no state
// event follows (setDialogState says nothing for an unchanged state).
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
	defer s.sendStart(id)(&err) // as relay.Seat: not quiet from Send's start to its delivered
	s.entered <- text
	<-s.gate
	s.fakeSeat.mu.Lock()
	s.fakeSeat.sent = append(s.fakeSeat.sent, text)
	s.fakeSeat.modes = append(s.fakeSeat.modes, mode)
	s.fakeSeat.mu.Unlock()
	s.emit(relay.Event{Kind: relay.KindInner, InnerID: "sess-new"},
		relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
	return "typed the command into pi", nil
}

// SR4-3. Stop session (app or web) in the loop step after the agent's seat
// has read a send's arrival in the transcript: the arrival is in the seat's
// events, waiting for the loop. stopSession kills the seat and settles its
// sends at once (settleExited), so the send is recorded "unconfirmed: the
// agent was stopped before its arrival showed in its transcript"; the
// arrival, posted after, is a dropped seat's news (watchSeat: no launch, no
// stopping entry). relay_control stop, its sibling, waits for the stopped
// seat to drain (drained) and settles after: there the arrival counts.
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
	waitFor(t, "the stopped seat drained", func() bool {
		gone := false
		rt.sync(func() {
			rt.relays.mu.Lock()
			_, held := rt.relays.drained[seat]
			rt.relays.mu.Unlock()
			gone = !held
		})
		return gone
	})
	got := deliveredFor(rt, id)
	t.Logf("outcomes of %s: %+v", id, got)
	var outcome state.SendOutcome
	rt.sync(func() { outcome = rt.st.SendOutcome(id) })
	if outcome != state.SendArrived {
		t.Errorf("the seat read %s arriving before the stop, but the record ends it %q (%v): the user and the next run are told it may not have reached the agent", id, outcome, got)
	}
}

// SR4-1. pi is idle. pi's own /new is sent with delivery after_turn (the
// phone's delivery choice, eagent.delivery, or relay_send's mode), and
// while it is being typed the user picks another effort in the thread's
// settings. pi is idle, so the seat types /new straight into its prompt
// as a command; no turn follows. The change is told to wait "when the turn
// that message starts ends" (heldBack calls the after_turn command "on its
// way": typedCommand is false for an after_turn send), and when the
// command's delivered comes, appendOutcome does not look at the change
// again (again typedCommand is false). Nothing else will: pi stays idle on
// its new session. The change waits until some later turn ends; on main a
// change to an idle agent restarted it at once.
func TestSR4AfterTurnCommandTypedIntoAnIdleAgentLeavesTheChangeWaitingForEver(t *testing.T) {
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
	// The seat says it is quiet right after the delivered (KindQuiet), and
	// that applies the change: a restart is under way once it is taken.
	var launching, waiting bool
	var st string
	var awaited int
	look := func() bool {
		rt.sync(func() {
			launching = rt.relays.launch("pi") != nil
			_, waiting = rt.relayRepick["pi"]
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

// SR4-3 through a whole session: Stop session while Codex's seat has read
// one send arriving and a move to another session of its own (its last
// look), and another send not yet shown. The record has, before
// session.end, the arrival and the move (a resume or restart opens that
// session), and the other send settled unconfirmed, once.
func TestK7StopSessionRecordsWhatTheSeatSawBeforeTheEnd(t *testing.T) {
	seat := newK1QuietSeat()
	rt, _, done := r10Session(t, seat)
	var ids []string
	for _, text := range []string{"run the tests", "then the linter"} {
		if out, isErr := rt.relayTool("relay_send", argStr(map[string]any{"text": text}), argNum(nil), argList(nil), false); isErr {
			t.Fatalf("relay_send: %s", out)
		}
		ids = append(ids, lastSendID(rt))
	}
	k1QueuesIdle(t, rt)
	rt.sync(func() {
		seat.emit(relay.Event{Kind: relay.KindInner, InnerID: "sess-moved"}, relay.Event{Kind: relay.KindDelivered, SendID: ids[0], OK: true})
		rt.stopSession("the user stopped the session")
	})
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string][]string{}
	moved, ended := false, false
	for _, ev := range evs {
		switch ev.Type {
		case event.SessionEnd:
			ended = true
		case event.RelayInner:
			var d event.RelayInnerData
			moved = moved || ev.Decode(&d) == nil && d.InnerID == "sess-moved" && !ended
		case event.RelayDelivered:
			var d event.RelayDeliveredData
			if ev.Decode(&d) == nil {
				o := d.Outcome
				if d.OK {
					o = "arrived"
				}
				if ended {
					o += " (after session.end)"
				}
				outcomes[d.ID] = append(outcomes[d.ID], o)
			}
		}
	}
	if got := outcomes[ids[0]]; len(got) != 1 || got[0] != "arrived" {
		t.Errorf("%s, read arriving before the stop: %v; want arrived, once, before session.end", ids[0], got)
	}
	if got := outcomes[ids[1]]; len(got) != 1 || got[0] != event.DeliveryUnconfirmed {
		t.Errorf("%s, not shown arriving: %v; want unconfirmed, once, before session.end", ids[1], got)
	}
	if !moved {
		t.Error("the move the stopped seat reported is not on the record before session.end")
	}
}
