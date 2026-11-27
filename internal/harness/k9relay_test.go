package harness

// The sixteenth review of the relay area, sixth pass (sr6).

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// SR6-1. Codex is in a long turn (a build, a test suite, a subagent: its
// transcript says nothing for ten minutes). The user's next message is
// typed into it while it works, and Codex holds it for its next step. The
// user then picks another model; the change is told it waits "when the
// turn ends". Ten minutes into the quiet the seat calls the agent blocked
// (no dialog), and since fbff884 the waiting change applies right then:
// the agent is killed mid-turn, and the message it held is reported
// unconfirmed (it may never reach the agent). On main a waiting change
// applied only at a turn's end or an idle state; blocked left it waiting.
func TestSR6AWaitingChangeKillsALongQuietTurn(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	seat.say(relay.Event{Kind: relay.KindState, State: "working"})
	k1Relay(t, rt, "the agent in a turn", func(rel *state.Relay) bool { return rel.State == "working" })
	_, id := k2Forward(t, rt, seat, "also update the changelog when you're done", "queue")
	msg := k1Repick(t, rt)
	if !strings.Contains(msg, "when the turn ends") {
		t.Fatalf("repick in a turn: %q", msg)
	}
	// Ten minutes of transcript silence inside the turn (checkScreen).
	seat.say(relay.Event{Kind: relay.KindState, State: "blocked", Detail: "no transcript activity for 10m; the screen ends:\n• Running go test ./... (12m 03s • esc to interrupt)"})
	// Decided in the step that records the block (k9: no 2 s wait).
	k1Relay(t, rt, "blocked recorded", func(rel *state.Relay) bool { return rel.State == "blocked" })
	restarted := k1Restarting(rt)
	if !restarted {
		return
	}
	t.Errorf("the change was told it waits until the turn ends (%q), but it restarted the agent while the turn was still running (blocked for transcript silence)", msg)
	seat.waitGone(t)
	seat.end()
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	waitFor(t, "the message's outcome", func() bool { return len(deliveredFor(rt, id)) > 0 })
	t.Errorf("the user's message Codex held for its next step: %q %+v", k2Outcome(rt, id), deliveredFor(rt, id))
}

// k9HalfSeat types a send until the test lets it go, then fails part way
// (its terminal was killed under it), as relay.Seat's TypingError says.
type k9HalfSeat struct {
	*fakeSeat
	entered chan string
	gate    chan struct{}
}

func (s *k9HalfSeat) Send(id, text, mode string) (_ string, err error) {
	defer s.sendStart(id)(&err)
	s.entered <- id
	<-s.gate
	return "", &relay.TypingError{Err: errors.New("can't find pane: k9")}
}

// The sixth review's suspicion: Stop session while a message is being
// typed into Codex. The stop's settle leaves the send being typed to
// settleSend (settleExited), but its step can run first, and the session
// ends before settleSend's step: the send, typed in part into an agent
// now gone, had no outcome before session.end. The stop settles after the
// stopped seat's send queue is done, as it waits for the seat's news.
func TestK9StopSessionSettlesASendBeingTypedBeforeTheEnd(t *testing.T) {
	seat := &k9HalfSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 1), gate: make(chan struct{})}
	rt, _, done := r10Session(t, seat)
	sent := make(chan string, 1)
	go func() {
		out, _ := rt.relayTool("relay_send", argStr(map[string]any{"text": "run the tests"}), argNum(nil), argList(nil), false)
		sent <- out
	}()
	id := <-seat.entered
	rt.sync(func() { rt.stopSession("the user stopped the session") })
	waitFor(t, "the stopped seat drained", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		_, held := rt.relays.drained[seat]
		return !held
	})
	close(seat.gate) // its terminal is gone: the typing fails part way
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	var outcomes []string
	ended := false
	for _, ev := range evs {
		var d event.RelayDeliveredData
		switch {
		case ev.Type == event.SessionEnd:
			ended = true
		case ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id:
			o := d.Outcome
			if ended {
				o += " (after session.end)"
			}
			outcomes = append(outcomes, o)
			t.Logf("%s: %s %q", id, d.Outcome, d.Detail)
		}
	}
	if len(outcomes) != 1 || outcomes[0] != event.DeliveryUnconfirmed {
		t.Errorf("%s, typed in part as Stop session killed Codex: %v; want one outcome, unconfirmed, before session.end", id, outcomes)
	}
}

// The sixth review's suspicion: pi ran /new just before a switch to Claude
// stopped it, and its seat says the move in its last look. The switch took
// that seat out of use, and the move was dropped as a replaced seat's news:
// a later restart of pi (relay_control restart of the stopped relay) would
// resume the session /new had left. The record keeps the move.
func TestK9AMoveSaidAtASwitchsCloseIsRecorded(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("claude")}, nil)
	rt := sr3PiRuntime(t)
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	seat.waitGone(t)
	seat.say(relay.Event{Kind: relay.KindInner, InnerID: "sess-new"}) // its last look
	seat.end()
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, switched, "switch")
	waitRelayStarts(t, rt)
	rt.sync(func() {
		if rel := rt.st.Relays["pi"]; rel == nil || rel.InnerID != "sess-new" {
			t.Errorf("pi on the record after the switch: %+v; want the session its /new opened, sess-new", rel)
		}
	})
}

// The sixth review's suspicion: Stop session while a model change restarts
// Codex. The restart took the old seat out of use, so Stop session found no
// seat to mark stopping, and the old seat's last look, which read a send
// arriving, was dropped as the session ended: the send was then settled
// unconfirmed where the start ended. Its arrival counts, as at a stop.
func TestK9StopSessionDuringARestartTakesTheOldSeatsArrival(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	rt.sync(func() {
		if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("restart: %v", err)
		}
	})
	seat.waitGone(t) // the restart stopped the old Codex; its seat drains
	rt.sync(func() { rt.stopSession("the user stopped the session") })
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true}) // its last look
	seat.end()
	g.waitEntered(t)
	g.release <- struct{}{}
	waitFor(t, "the send's outcome", func() bool { return len(deliveredFor(rt, id)) > 0 })
	if got := deliveredFor(rt, id); len(got) != 1 || !got[0].OK {
		t.Errorf("%s, read arriving by the old seat: %+v; want its one outcome, arrived", id, got)
	}
}
