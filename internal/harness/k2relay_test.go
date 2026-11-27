package harness

import (
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// k2FeedSeat is an agent whose seat's news the test hands over one event
// at a time (its events are unbuffered) and ends by closing them, as a
// real seat's watcher does once the seat is killed. It shows no arrival of
// what it is sent.
type k2FeedSeat struct {
	*fakeSeat
	feed       chan relay.Event
	gone       chan struct{}
	once, done sync.Once
}

func newK2FeedSeat(t *testing.T) *k2FeedSeat {
	s := &k2FeedSeat{fakeSeat: newFakeSeat("one"), feed: make(chan relay.Event), gone: make(chan struct{})}
	t.Cleanup(s.end)
	return s
}

func (s *k2FeedSeat) Events() <-chan relay.Event { return s.feed }

func (s *k2FeedSeat) Send(id, text, mode string) (string, error) {
	s.mu.Lock()
	s.sent = append(s.sent, text)
	s.modes = append(s.modes, mode)
	s.mu.Unlock()
	if mode == "after_turn" {
		return "held for Codex to read when its turn ends", nil
	}
	return "typed into Codex", nil
}

func (s *k2FeedSeat) Kill() error {
	s.once.Do(func() { close(s.gone) })
	return nil
}

func (s *k2FeedSeat) Close() {}

// say hands the seat's watcher one event after another.
func (s *k2FeedSeat) say(evs ...relay.Event) {
	for _, ev := range evs {
		s.feed <- ev
	}
}

// end closes the seat's events: it has nothing more to say.
func (s *k2FeedSeat) end() { s.done.Do(func() { close(s.feed) }) }

func (s *k2FeedSeat) waitGone(t *testing.T) {
	t.Helper()
	select {
	case <-s.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent was never stopped")
	}
}

// k2Until polls cond for at most d and says whether it held.
func k2Until(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// k2Forward has the user's message forwarded with delivery mode and
// returns its seq and send id once the seat's queue has typed it.
func k2Forward(t *testing.T, rt *Runtime, seat *k2FeedSeat, text, mode string) (int64, string) {
	t.Helper()
	var seq int64
	rt.sync(func() {
		d := event.UserMessageData{Text: text, Delivery: mode}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		seq = ev.Seq
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("not routed: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
	waitFor(t, "the message typed", func() bool { return len(seat.sentTexts()) == 1 })
	k1QueuesIdle(t, rt)
	return seq, lastSendID(rt)
}

func k2Outcome(rt *Runtime, id string) (o state.SendOutcome) {
	rt.sync(func() { o = rt.st.SendOutcome(id) })
	return o
}

// Review 15, finding 5. A message held for the end of Codex's turn is
// swept from the spool unread (not sent) as a model change restarts Codex,
// or a switch replaces it: the seat says so before its events close. That
// outcome is the send's (not "unconfirmed", nor held for good), and the
// user's message may be forwarded again.
func TestK2ANotSentFromTheSeatARestartReplacesIsKept(t *testing.T) {
	for _, how := range []string{"restart", "switch"} {
		seat := newK2FeedSeat(t)
		g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
		rt := newLaunchRuntime(t)
		seq, id := k2Forward(t, rt, seat, "then run the linter", "after_turn")
		var switched <-chan string
		if how == "restart" {
			k1Repick(t, rt)
		} else {
			switched = relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
		}
		seat.waitGone(t)
		// The second event is taken only once the first is posted; the sync
		// then runs after it: the outcome reached the loop before the new
		// agent is up.
		seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Outcome: relay.OutcomeNotSent, Detail: "it was still in the spool when the turn ended; taken back unread"},
			relay.Event{Kind: relay.KindActivity, Text: "closing"})
		rt.sync(func() {})
		seat.end()
		g.release <- struct{}{}
		if switched != nil {
			recv(t, switched, "switch")
		}
		waitRelayStarts(t, rt)
		if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
			t.Fatalf("%s: %s: %+v; want its one outcome, not_sent", how, id, got)
		}
		var prev, why string
		rt.sync(func() { prev, why = forwardedAs(rt.st, "codex", seq, "") })
		if prev != "" {
			t.Errorf("%s: forwarding the message again is refused as %s: %q", how, prev, why)
		}
	}
}

// Review 15, finding 6. Codex's transcript showed a message arriving just
// as a model change restarted it; the seat had that arrival in hand when
// it was killed, and its watcher hands it over only after the new agent is
// up. The restart waits for the old seat's news: the message arrived, and
// it is not also reported unconfirmed.
func TestK2AnArrivalTheReplacedSeatStillHadIsKept(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	g.release <- struct{}{} // the new agent comes up at once
	unblock := make(chan struct{})
	rt.post(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
		<-unblock // the loop is busy: what reaches it now waits, in order
	})
	seat.waitGone(t)
	// Whatever the restart has the loop do next is queued before the old
	// seat's arrival (a restart that does not wait for it).
	k2Until(100*time.Millisecond, func() bool { return len(rt.loop) > 0 })
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
	seat.end()
	close(unblock)
	waitRelayStarts(t, rt)
	if got := deliveredFor(rt, id); len(got) != 1 || !got[0].OK || k2Outcome(rt, id) != state.SendArrived {
		t.Fatalf("%s: %+v; want its one outcome, arrived", id, got)
	}
}
