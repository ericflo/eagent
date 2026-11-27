package harness

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// SR13-1. The user's message is held for the end of Codex's turn (after_turn,
// in the spool). The user quits (the agent keeps running, for a resume)
// while a turn of eagent's is still finishing. In that window Codex's turn
// ends and its seat, finding the message still unclaimed, takes it out of
// the spool and says it was not sent (relay.Seat leftOvers). The seat is
// still the relay's own (a quit retires nothing), so its word goes through
// onRelayEvent, which drops everything once the session is ending. The
// message is gone from the spool and the record never says so: the send has
// no outcome at session.end, and a resume can only call it unconfirmed.
func TestSR13RelaySeatsNotSentDuringTheEndingIsDropped(t *testing.T) {
	seat := newK2FeedSeat(t)
	rt, _, done := r10Session(t, seat)
	_, id := k2Forward(t, rt, seat, "and update the docs", "after_turn")

	rt.wg.Add(1) // a turn of eagent's still finishing as the user quits
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Outcome: relay.OutcomeNotSent,
		Detail: "Codex's turn ended before it took this message, so it was not delivered; send it again if it is still wanted"})
	// The watcher hands each event to the loop before it reads the next.
	seat.say(relay.Event{Kind: relay.KindActivity, Text: "idle"})
	rt.wg.Done()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	var got []event.RelayDeliveredData
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id {
			got = append(got, d)
		}
	}
	if len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
		t.Fatalf("the send %s that the seat took back from the spool and said not sent: outcomes on the record %+v; want one, not_sent", id, got)
	}
}
