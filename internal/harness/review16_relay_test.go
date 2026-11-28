package harness

// The sixteenth review's findings on what a stopped, restarted or replaced
// agent's terminal still says of its sends, and on a switch's brief.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// A message of the user's, typed into Codex and not yet seen arriving, is
// quoted whole in the brief of the switch to Claude. Codex's terminal then
// says the message did not go, or could not be confirmed. The brief gave
// it to Claude: nothing is recorded of it, and eagent is not told to send
// it again (which would give it to Claude twice).
func TestAReplacedAgentsFailureOnAMessageItsBriefCarriesIsNotRecorded(t *testing.T) {
	for _, outcome := range []string{relay.OutcomeNotSent, relay.OutcomeUnconfirmed} {
		t.Run(outcome, func(t *testing.T) {
			seat := newK2FeedSeat(t)
			next := newFakeSeat("two")
			g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
			rt := newLaunchRuntime(t)
			_, id := k2Forward(t, rt, seat, "then run the linter", "queue")
			g.release <- struct{}{} // the new agent comes up at once
			switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
			seat.waitGone(t)
			waitRelayStarts(t, rt)
			recv(t, switched, "switch")
			waitFor(t, "the brief typed", func() bool { return len(next.sentTexts()) > 0 })
			if brief := next.sentTexts()[0]; !strings.Contains(brief, "- then run the linter\n") {
				t.Fatalf("setup: the brief does not quote the message:\n%s", brief)
			}
			seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Outcome: outcome, Detail: "the turn ended before it took this message"})
			seat.end()
			rt.sync(func() {})
			var tail string
			rt.sync(func() { tail = rt.attentionTail("Claude") })
			if got := deliveredFor(rt, id); len(got) != 0 {
				t.Errorf("the brief gave Claude the message, and the record says of it %+v", got)
			}
			if strings.Contains(tail, "Send it again") || strings.Contains(tail, "send it again") {
				t.Errorf("eagent is told: %q", tail)
			}
		})
	}
}

// The brief that quotes the user's message is still being typed into
// Claude when Codex's terminal says the message did not go (or says
// nothing more), and the brief then cannot be typed. Neither agent has the
// message: the record says it did not go, and eagent is told to pass it
// on, not that it waits to be taken up.
func TestAMessageWhoseBriefDidNotGoIsNotLeftWaiting(t *testing.T) {
	for _, said := range []string{"the old terminal says it did not go", "the old terminal says nothing"} {
		t.Run(said, func(t *testing.T) {
			seat := newK2FeedSeat(t)
			inner := &k2FeedSeat{fakeSeat: newFakeSeat("two"), feed: make(chan relay.Event), gone: make(chan struct{})}
			t.Cleanup(inner.end)
			next := &k33StuckSeat{k2FeedSeat: inner, entered: make(chan string, 1), release: make(chan error, 1)}
			g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
			rt := newLaunchRuntime(t)
			_, id := k2Forward(t, rt, seat, "then run the linter", "queue")
			g.release <- struct{}{}
			switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
			seat.waitGone(t)
			waitRelayStarts(t, rt)
			select {
			case brief := <-next.entered:
				if !strings.Contains(brief, "- then run the linter\n") {
					t.Fatalf("setup: the brief does not quote the message:\n%s", brief)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the brief was never being typed")
			}
			if said == "the old terminal says it did not go" {
				seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Outcome: relay.OutcomeNotSent, Detail: "the turn ended before it took this message"})
			}
			seat.end()
			rt.sync(func() {})
			if got := deliveredFor(rt, id); len(got) != 0 {
				t.Fatalf("while the brief is on its way the record says of the message %+v", got)
			}
			next.release <- errors.New("the terminal took nothing")
			recv(t, switched, "switch")
			k1QueuesIdle(t, rt)
			var user string
			rt.sync(func() { user = rt.userTail("Claude") })
			if o := k2Outcome(rt, id); o != state.SendNotSent {
				t.Errorf("neither agent has the message, and the record says of it: %q", o)
			}
			if strings.Contains(user, "waits to be taken up") || !strings.Contains(user, "did not go") {
				t.Errorf("eagent is told: %q", user)
			}
		})
	}
}

// r16RefusingSeat is an agent whose terminal takes nothing.
type r16RefusingSeat struct{ *fakeSeat }

func (s *r16RefusingSeat) Send(id, text, mode string) (string, error) {
	return "", errors.New("the terminal took nothing")
}

// relay_forward is still typing the user's message into Codex when a
// switch to Claude goes on without it, and the brief that quotes it whole
// cannot be typed into Claude. The typing into Codex then ends with its
// terminal closed. Neither agent has the message: the model is told it
// could not be sent, and the record says so.
func TestAForwardLeftToABriefThatDidNotGoIsNotSent(t *testing.T) {
	setFor(t, &oldSendSettle, 50*time.Millisecond)
	seat := &k33StuckSeat{k2FeedSeat: newK2FeedSeat(t), entered: make(chan string, 1), release: make(chan error, 1)}
	next := &r16RefusingSeat{newFakeSeat("two")}
	g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
	rt := newLaunchRuntime(t)
	var seq int64
	rt.sync(func() {
		seq = rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "fix the flaky test please"})).Seq
	})
	fwd := make(chan string, 1)
	go func() {
		text, isErr := forward(rt, map[string]any{"seq": seq})
		fwd <- fmt.Sprintf("%v %s", isErr, text)
	}()
	select {
	case <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the message was never being typed")
	}
	id := lastSendID(rt)
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	seat.waitGone(t)
	seat.end()
	g.release <- struct{}{}
	if out := recv(t, switched, "switch"); !strings.Contains(out, "the handoff brief could not be typed") {
		t.Fatalf("setup: the switch said %q", out)
	}
	seat.release <- relay.ErrClosed
	out := recv(t, fwd, "relay_forward")
	k1QueuesIdle(t, rt)
	if !strings.HasPrefix(out, "true ") || strings.Contains(out, "quotes the message whole") {
		t.Errorf("neither agent has the message, and the model was told: %q", out)
	}
	if o := k2Outcome(rt, id); o != state.SendNotSent {
		t.Errorf("the record says of %s: %q, want not sent", id, o)
	}
}

// The user's message is typed into Codex as it arrives, and Codex is then
// stopped (or restarted) before the message is seen arriving; its old
// terminal reports the arrival before eagent's model looks. The model is
// not told to say the message reached the agent: it is told where the
// message arrived, and to check an agent running now.
func TestALateArrivalOfTheUsersOwnMessageIsNotCalledReached(t *testing.T) {
	for _, how := range []string{"stop", "restart"} {
		t.Run(how, func(t *testing.T) {
			seat := newK2FeedSeat(t)
			g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
			rt := newLaunchRuntime(t)
			var seq int64
			rt.sync(func() {
				d := event.UserMessageData{Text: "run the tests"}
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
			id := lastSendID(rt)
			if how == "stop" {
				k1Stop(t, rt)
			} else {
				g.release <- struct{}{}
				restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
				seat.waitGone(t)
				waitRelayStarts(t, rt)
				recv(t, restarted, "restart")
			}
			if o := k2Outcome(rt, id); o != state.SendUnconfirmed {
				t.Fatalf("setup: %s after the %s: %q, want unconfirmed", id, how, o)
			}
			seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
			seat.end()
			waitFor(t, "the arrival recorded", func() bool { return k2Outcome(rt, id) == state.SendArrived })
			var tail string
			rt.sync(func() {
				if rt.turns.wakes[wakeUser] != seq {
					t.Errorf("setup: the user's message is not what the look is owed for")
				}
				tail = rt.tail(wake{event: true}, time.Now())
			})
			if strings.Contains(tail, "Say in one short line that it reached") || !strings.Contains(tail, "did arrive in the agent it was typed into") || !strings.Contains(tail, "whether an agent running now is acting on it") {
				t.Errorf("after the %s and the old terminal's late arrival, the model is told:\n%s", how, tail)
			}
		})
	}
}

// eagent's own message to Codex is settled as not confirmed when Codex is
// restarted, and the model has looked at that. The old terminal then
// reports the arrival: the model is woken for it and told.
func TestALateArrivalAfterTheModelSawTheOlderWordWakesIt(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	if out, isErr := rt.relayTool("relay_send", argStr(map[string]any{"text": "run the tests"}), argNum(nil), argList(nil), false); isErr {
		t.Fatalf("relay_send: %s", out)
	}
	id := lastSendID(rt)
	k1QueuesIdle(t, rt)
	g.release <- struct{}{}
	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	seat.waitGone(t)
	waitRelayStarts(t, rt)
	recv(t, restarted, "restart")
	if o := k2Outcome(rt, id); o != state.SendUnconfirmed {
		t.Fatalf("setup: %s after the restart: %q, want unconfirmed", id, o)
	}
	rt.sync(func() { rt.turns.seen = rt.st.LastSeq() }) // the model has looked
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
	seat.end()
	waitFor(t, "the arrival recorded", func() bool { return k2Outcome(rt, id) == state.SendArrived })
	var pending bool
	var tail string
	rt.sync(func() {
		pending = rt.wakePending()
		tail = rt.tail(wake{event: true}, time.Now())
	})
	if !pending || !strings.Contains(tail, "did arrive in the agent it was typed into") {
		t.Errorf("the late arrival is recorded; a look is owed: %v; the model would be told:\n%s", pending, tail)
	}
}

// Codex is stopped with the user's message not confirmed, and its old
// terminal reports the arrival, before or after a fresh Codex is started
// under the name. The fresh Codex was never sent the message: a forward
// of it is refused in words that say so, not as one that already went to
// the Codex running now.
func TestALateArrivalDoesNotMakeAStoppedAgentsMessageTheFreshAgents(t *testing.T) {
	const fresh = "that message already went to Codex as written"
	for _, order := range []string{"before the fresh start", "after the fresh start"} {
		t.Run(order, func(t *testing.T) {
			rec := &relayRecord{t: time.Now()}
			rec.start("codex", "codex")
			seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
			rec.send("codex", "r1", seq)
			rec.add(event.RelayEnd, event.RelayEndData{Name: "codex", Reason: "stopped"})
			rec.add(event.RelayDelivered, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: false, Outcome: event.DeliveryUnconfirmed, Detail: "the agent was stopped before its transcript showed the message"})
			late := func() { rec.add(event.RelayDelivered, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: true}) }
			if order == "before the fresh start" {
				late()
			}
			rec.start("codex", "codex")
			if order == "after the fresh start" {
				late()
			}
			st := state.Replay(rec.evs)
			id, why := forwardedAs(st, "codex", seq, "")
			if id != "r1" || strings.Contains(why, fresh) || !strings.Contains(why, "to the agent that held the name codex before, which was stopped") {
				t.Errorf("a forward to the fresh Codex is refused as %q: %q", id, why)
			}
		})
	}
}
