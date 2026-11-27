package harness

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// The round-16 reader's second test (rv16_relay_test.go), with
// oldSendSettle shortened. A restart of an idle agent whose seat's events
// never end after the kill (its watcher is stuck): the start waits on the
// drain with no bound (3a8f6a8), where the wait on the old send just
// before it is bounded (oldSendSettle).
func TestRV16RestartDoesNotWaitForEverOnAStuckWatcher(t *testing.T) {
	setFor(t, &oldSendSettle, 200*time.Millisecond)
	feed := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{feed, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	marker := relay.Event{Kind: relay.KindActivity, Text: "…"}
	feed.say(relay.Event{Kind: relay.KindState, State: "idle"}, marker)
	rt.sync(func() {})
	k1Repick(t, rt)
	feed.waitGone(t)
	select {
	case <-g.entered:
	case <-time.After(oldSendSettle + 3*time.Second):
		t.Fatalf("killed %v ago, the new agent was never launched: the start waits on the old seat's drain with no bound", oldSendSettle+3*time.Second)
	}
	feed.end()
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
}

// The same for a stop (19fa34b waits for the stopped seat's drain): a
// stopped agent whose watcher is stuck does not hold the stop for ever; it
// settles what is left after the same bound, once.
func TestK3AStopDoesNotWaitForEverOnAStuckWatcher(t *testing.T) {
	setFor(t, &oldSendSettle, 200*time.Millisecond)
	feed := newK2FeedSeat(t)
	useGatedLaunches(t, []relaySeat{feed}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, feed, "run the tests", "queue")
	stopped := relayToolAsync(rt, map[string]any{"action": "stop"})
	feed.waitGone(t)
	select {
	case <-stopped:
	case <-time.After(oldSendSettle + 3*time.Second):
		t.Fatalf("the stop waits on the stopped seat's drain with no bound")
	}
	if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed {
		t.Fatalf("%s: %+v; want its one outcome, unconfirmed", id, got)
	}
}
