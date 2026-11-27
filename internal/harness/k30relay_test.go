package harness

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// k30Outcomes waits for the session's end and returns every outcome its
// record has for the send.
func k30Outcomes(t *testing.T, rt *Runtime, done <-chan int, id string) []event.RelayDeliveredData {
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
	var got []event.RelayDeliveredData
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id {
			got = append(got, d)
		}
	}
	return got
}

func k30QueueEnded(t *testing.T, rt *Runtime) {
	t.Helper()
	waitFor(t, "the loop's queue ended", func() bool {
		rt.queueMu.RLock()
		defer rt.queueMu.RUnlock()
		return rt.queueEnded
	})
}

func k30Settle(t *testing.T, d time.Duration) {
	saved := oldSendSettle
	oldSendSettle = d
	t.Cleanup(func() { oldSendSettle = saved })
}

// A stopped agent whose seat has not said all it saw past the stop's bound
// is settled as it stands (not confirmed); what the seat says later is
// logged, not recorded. That holds when it says it once the loop's queue
// has ended as well: the send keeps the one outcome it was settled with.
func TestK30ASettledSeatsWordAfterTheQueueEndedIsNotRecorded(t *testing.T) {
	k30Settle(t, 200*time.Millisecond)
	seat := newK2FeedSeat(t)
	rt, _, done := r10Session(t, seat)
	_, id := k2Forward(t, rt, seat, "and update the docs", "queue")
	if out, _ := rt.relayTool("relay_control", argStr(map[string]any{"action": "stop"}), argNum(nil), argList(nil), false); out == "" {
		t.Fatal("no answer from stop")
	}
	waitFor(t, "the send settled as it stands", func() bool { return k2Outcome(rt, id) == state.SendUnconfirmed })
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	k30QueueEnded(t, rt)
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true, Read: 50})
	seat.end()
	if got := k30Outcomes(t, rt, done, id); len(got) != 1 || got[0].OK {
		t.Fatalf("the record has %+v for the send; want the one outcome it was settled with", got)
	}
}

// A seat taken out of use for a restart is settled where the new start
// ends, which the session's end may overtake: the start is then recorded,
// and the old seat settled, once the loop's queue has ended. What the seat
// said of a send before that (it arrived) is on the record before the
// settle, which finds the send with its outcome: arrived, and nothing more.
func TestK30ASeatsWordSaidBeforeItsSettleIsRecordedFirst(t *testing.T) {
	k30Settle(t, 100*time.Millisecond)
	seat := newK2FeedSeat(t)
	rt, _, done := r10Session(t, seat)
	_, id := k2Forward(t, rt, seat, "and update the docs", "queue")
	rt.relayOps.Add(1) // the start under way: finish waits for it, its queue ended
	var rel *state.Relay
	rt.sync(func() {
		rel = rt.st.Relays["codex"]
		rt.retire("codex", seat, rel, nil) // as a restart does; its start says how the sends are settled
		rt.beginShutdown("quit", 0)
	})
	k30QueueEnded(t, rt)
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true, Read: 50})
	waitFor(t, "the seat's word kept for the end", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		return len(rt.relays.lateSaid) == 1
	})
	// The overtaken start's settle, as finish runs it (settleOld leaves it
	// with the seat; settleRestRetired). Finish waits for the start
	// meanwhile, so nothing else writes.
	rt.relays.mu.Lock()
	rt.relays.retiring[seat].settle = func() { rt.settleExited(rel) }
	rt.relays.mu.Unlock()
	rt.settleRetired(seat)
	rt.relayOps.Done()
	if got := k30Outcomes(t, rt, done, id); len(got) != 1 || !got[0].OK {
		t.Fatalf("the record has %+v for the send; want one outcome, arrived, as the seat said before its sends were settled", got)
	}
}
