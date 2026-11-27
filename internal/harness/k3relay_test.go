package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// Item 1, its bound. While the stop waits for the stopped seat's news, a
// restart is asked for and a message is sent (held for the restart). The
// stop settles only what the stopped agent was typed; the held message is
// the restart's, typed into the new agent and confirmed there, once.
func TestK3AMessageHeldForARestartDuringAStopIsTheRestarts(t *testing.T) {
	seat := newK2FeedSeat(t)
	next := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
	rt := newLaunchRuntime(t)
	_, typed := k2Forward(t, rt, seat, "run the tests", "queue")
	stopped := relayToolAsync(rt, map[string]any{"action": "stop"})
	seat.waitGone(t)
	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	waitFor(t, "the restart under way", func() bool { return k1Restarting(rt) })
	held := make(chan string, 1)
	go func() {
		out, _ := rt.relayTool("relay_send", argStr(map[string]any{"text": "and the linter"}), argNum(nil), argList(nil), false)
		held <- out
	}()
	recv(t, held, "relay_send")
	heldID := lastSendID(rt)
	seat.end()
	recv(t, stopped, "stop")
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, restarted, "restart")
	waitRelayStarts(t, rt)
	waitFor(t, "the held message in the new agent", func() bool { return len(next.sentTexts()) == 1 })
	waitFor(t, "its arrival", func() bool { return len(deliveredFor(rt, heldID)) > 0 })
	if got := deliveredFor(rt, typed); len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed {
		t.Errorf("%s typed into the stopped agent: %+v; want one unconfirmed", typed, got)
	}
	if got := deliveredFor(rt, heldID); len(got) != 1 || !got[0].OK {
		t.Errorf("%s held for the restart: %+v; want its one outcome, arrived", heldID, got)
	}
}

// Item 1. A stop of Codex while its seat still has a message's arrival in
// hand (its watcher hands it over only as the stop closes it): the arrival
// is the message's outcome, alone, as on main. With nothing in hand, the
// stop reports the message unconfirmed, once.
func TestK3AnArrivalTheStoppedSeatHadIsKept(t *testing.T) {
	for _, arrives := range []bool{true, false} {
		seat := newK2FeedSeat(t)
		useGatedLaunches(t, []relaySeat{seat}, nil)
		rt := newLaunchRuntime(t)
		_, id := k2Forward(t, rt, seat, "run the tests", "queue")
		stopped := relayToolAsync(rt, map[string]any{"action": "stop"})
		seat.waitGone(t)
		if arrives {
			seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
		}
		seat.end()
		recv(t, stopped, "stop")
		waitFor(t, "an outcome", func() bool { return len(deliveredFor(rt, id)) > 0 })
		got := deliveredFor(rt, id)
		switch {
		case arrives && (len(got) != 1 || !got[0].OK || k2Outcome(rt, id) != state.SendArrived):
			t.Fatalf("%s: %+v; want its one outcome, arrived", id, got)
		case !arrives && (len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed):
			t.Fatalf("%s: %+v; want its one outcome, unconfirmed", id, got)
		}
	}
}
