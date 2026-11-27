package harness

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// k27ReadMarkStays ends the session and checks the record: the send has its
// one outcome, and where a reattach reads on from is where it was before
// the ending (lastNews): the relay's read mark does not stand past a turn
// the record does not have, and its last news is no later.
func k27ReadMarkStays(t *testing.T, rt *Runtime, done <-chan int, id string, ok bool, lastNews time.Time) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	st := state.New()
	turns := 0
	var got []event.RelayDeliveredData
	for _, ev := range evs {
		st.Apply(ev)
		if ev.Type == event.RelayTurn {
			turns++
		}
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id {
			got = append(got, d)
		}
	}
	if len(got) != 1 || got[0].OK != ok || !ok && got[0].Outcome != event.DeliveryNotSent {
		t.Fatalf("send %s: outcomes on the record %+v; want one (arrived: %v, else not_sent)", id, got, ok)
	}
	rel := st.Relays["codex"]
	if rel == nil {
		t.Fatal("no codex relay on the record")
	}
	if turns == 0 && rel.Read >= 100 {
		t.Fatalf("the record has no relay.turn for Codex's finished turn (its report dropped at the end), yet its read mark is %d, past that turn's line (100): a resume reattaching reads on from %d and takes the turn as history, so the agent's report is lost", rel.Read, rel.Read)
	}
	if turns == 0 && rel.LastEventAt.After(lastNews) {
		t.Fatalf("the record has no relay.turn for Codex's finished turn, yet the relay's last news moved from %s to %s: a reattach that goes by time takes the turn as history", lastNews.Format(time.RFC3339Nano), rel.LastEventAt.Format(time.RFC3339Nano))
	}
}

// The user quits while Codex works and a message is held for the end of
// its turn; a start or stop of another agent is still under way, so the
// loop's queue ends before the seats are closed. Codex's turn ends (its
// report, read up to byte 100 of the transcript, is dropped: the session is
// ending), and later its seat takes the held message back and says so, with
// the read mark it has then (200, past the turn's line). The not_sent is
// written by finish without that mark and at the relay's last news, so a
// resume reads the turn again, as on main, where nothing was recorded
// during the end. (It was written with the mark: a resume read on from 200
// and the agent's report was never seen.)
func TestK27ALateNotSentLeavesTheReadMarkBeforeADroppedTurn(t *testing.T) {
	seat := newK2FeedSeat(t)
	rt, _, done := r10Session(t, seat)
	_, id := k2Forward(t, rt, seat, "and update the docs", "after_turn")

	rt.relayOps.Add(1) // a start or stop of another agent still under way
	var lastNews time.Time
	rt.sync(func() {
		lastNews = rt.st.Relays["codex"].LastEventAt
		rt.beginShutdown("quit", 0)
	})
	waitFor(t, "the loop's queue ended", func() bool {
		rt.queueMu.RLock()
		defer rt.queueMu.RUnlock()
		return rt.queueEnded
	})
	seat.say(relay.Event{Kind: relay.KindTurn, Text: "All tests pass; I fixed the flaky one in parse_test.go.", Read: 100})
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Outcome: relay.OutcomeNotSent, Read: 200,
		Detail: "Codex's turn ended before it took this message, so it was not delivered; send it again if it is still wanted"})
	rt.relayOps.Done()
	k27ReadMarkStays(t, rt, done, id, false, lastNews)
}

// The same through the ending path that runs while finish still reads the
// queue (onRelayEvent, r.ending): the user quits while a turn of eagent's is
// in flight; Codex's turn ends (dropped) and the Stop hook hands it the held
// message, whose arrival (read mark 200, past the turn's line at 100) is
// recorded, without the mark.
func TestK27AnArrivalDuringTheEndLeavesTheReadMarkBeforeADroppedTurn(t *testing.T) {
	seat := newK2FeedSeat(t)
	rt, _, done := r10Session(t, seat)
	_, id := k2Forward(t, rt, seat, "and update the docs", "after_turn")

	rt.wg.Add(1) // a turn of eagent's in flight: finish reads the queue while it waits
	var lastNews time.Time
	rt.sync(func() {
		lastNews = rt.st.Relays["codex"].LastEventAt
		rt.beginShutdown("quit", 0)
	})
	select {
	case <-rt.loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never ended")
	}
	seat.say(relay.Event{Kind: relay.KindTurn, Text: "All tests pass; I fixed the flaky one in parse_test.go.", Read: 100})
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true, Read: 200})
	waitFor(t, "the arrival recorded", func() bool {
		evs, err := store.Read(rt.sess.Path)
		if err != nil {
			return false
		}
		return state.Replay(evs).SendOutcome(id) == state.SendArrived
	})
	rt.wg.Done()
	k27ReadMarkStays(t, rt, done, id, true, lastNews)
}
