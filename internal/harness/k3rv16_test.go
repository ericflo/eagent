package harness

// The round-16 reader's tests of the relay fixes (copied from its
// rv16_relay_test.go; the drain test shortens oldSendSettle).

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// rv16FailSeat types nothing until released, then fails the send (the
// prompt never came back): the harness records the send not sent itself.
type rv16FailSeat struct {
	*k2FeedSeat
	release chan struct{}
}

func (s *rv16FailSeat) Send(id, text, mode string) (_ string, err error) {
	s.mu.Lock()
	s.sent = append(s.sent, text)
	s.mu.Unlock()
	<-s.release
	return "", errors.New("Codex's prompt did not come back")
}

// A change waits for the turn; a message is being typed when the turn
// ends, so the change waits for it (c519ce4). The typing then fails while
// the agent is idle: nothing is on its way any more and no turn will start.
// The change must apply then.
func TestRV16WaitingChangeAppliesWhenTypingFailsWhileIdle(t *testing.T) {
	feed := newK2FeedSeat(t)
	seat := &rv16FailSeat{k2FeedSeat: feed, release: make(chan struct{})}
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	marker := relay.Event{Kind: relay.KindActivity, Text: "…"}
	feed.say(relay.Event{Kind: relay.KindState, State: "working"}, marker)
	rt.sync(func() {})
	if msg := k1Repick(t, rt); !strings.Contains(msg, "when the turn ends") {
		t.Fatalf("repick in a turn: %q", msg)
	}
	rt.sync(func() {
		d := event.UserMessageData{Text: "then run the linter", Delivery: "queue"}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("not routed: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
	waitFor(t, "the message being typed", func() bool { return len(seat.sentTexts()) == 1 })
	id := lastSendID(rt)
	feed.say(relay.Event{Kind: relay.KindTurn, Text: "Built it."}, relay.Event{Kind: relay.KindState, State: "idle"}, marker)
	rt.sync(func() {})
	close(seat.release)
	waitFor(t, "the send's outcome", func() bool { return len(deliveredFor(rt, id)) == 1 })
	if got := deliveredFor(rt, id); got[0].OK {
		t.Fatalf("outcome %+v", got[0])
	}
	if !k2Until(3*time.Second, func() bool { return k1Restarting(rt) }) {
		var waiting bool
		var state string
		rt.sync(func() { _, waiting = rt.waitingPick("codex"); state = rt.st.Relays["codex"].State })
		t.Fatalf("idle (%s), nothing awaited, change still waiting=%v: it is never applied", state, waiting)
	}
	feed.waitGone(t)
	feed.end()
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
}
