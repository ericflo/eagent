package harness

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// k29Arrived is a session whose agent has a message of the user's on the
// record as arrived (its queue took it).
func k29Arrived(t *testing.T) (*Runtime, *k2FeedSeat, <-chan int, string) {
	t.Helper()
	seat := newK2FeedSeat(t)
	rt, _, done := r10Session(t, seat)
	_, id := k2Forward(t, rt, seat, "and update the docs", "queue")
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true, Read: 50})
	waitFor(t, "the arrival recorded", func() bool { return k2Outcome(rt, id) == state.SendArrived })
	return rt, seat, done, id
}

// k29NotConfirmed waits for the session's end and wants the send's outcome
// on the record to be the seat's newer word (not confirmed), with the
// relay's read mark and last news where they stood.
func k29NotConfirmed(t *testing.T, rt *Runtime, done <-chan int, id string, read int64, lastNews time.Time) {
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
	st := state.Replay(evs)
	if got := st.SendOutcome(id); got != state.SendUnconfirmed {
		t.Fatalf("the record has the send %q; want not confirmed, the seat's newer word on a message its agent's queue gave up unread", got)
	}
	if rel := st.Relays["codex"]; rel == nil || rel.Read != read || !rel.LastEventAt.Equal(lastNews) {
		t.Fatalf("the relay's read mark and last news moved: %+v; want %d and %s", rel, read, lastNews.Format(time.RFC3339Nano))
	}
}

const k29GaveUp = "was taken into the agent's queue, which gave it up unread, and no prompt had shown it when the agent's seat was closed"

// The agent's queue gave a message back unread, and its seat says so as
// its last word, after the loop's queue has ended. The record had the
// message arrived; the session's end writes the seat's newer word.
func TestK29ASeatsLastWordOnAnArrivedSendIsRecordedAtTheEnd(t *testing.T) {
	rt, seat, done, id := k29Arrived(t)
	rt.relayOps.Add(1) // a start or stop of another agent still under way
	var read int64
	var lastNews time.Time
	rt.sync(func() {
		read, lastNews = rt.st.Relays["codex"].Read, rt.st.Relays["codex"].LastEventAt
		rt.beginShutdown("quit", 0)
	})
	waitFor(t, "the loop's queue ended", func() bool {
		rt.queueMu.RLock()
		defer rt.queueMu.RUnlock()
		return rt.queueEnded
	})
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Outcome: relay.OutcomeUnconfirmed, Detail: k29GaveUp})
	rt.relayOps.Done()
	k29NotConfirmed(t, rt, done, id, read, lastNews)
}

// The same word said while the session is ending and its loop still takes
// what seats say.
func TestK29ASeatsNewerWordOnAnArrivedSendIsRecordedDuringTheEnd(t *testing.T) {
	rt, seat, done, id := k29Arrived(t)
	rt.wg.Add(1) // a turn of eagent's in flight: finish reads the queue while it waits
	var read int64
	var lastNews time.Time
	rt.sync(func() {
		read, lastNews = rt.st.Relays["codex"].Read, rt.st.Relays["codex"].LastEventAt
		rt.beginShutdown("quit", 0)
	})
	select {
	case <-rt.loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never ended")
	}
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Outcome: relay.OutcomeUnconfirmed, Detail: k29GaveUp})
	waitFor(t, "the seat's word recorded", func() bool {
		evs, err := store.Read(rt.sess.Path)
		return err == nil && state.Replay(evs).SendOutcome(id) == state.SendUnconfirmed
	})
	rt.wg.Done()
	k29NotConfirmed(t, rt, done, id, read, lastNews)
}
