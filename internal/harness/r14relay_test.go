package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// Finding 10. A send that waited for a switch from Claude to Codex and
// then certainly did not go to Codex did not go anywhere: Claude's list
// says so, and the user's message may be forwarded to Claude again.
func TestR14AMovedSendThatDidNotGoIsNotSent(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("claude", "claude")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("claude", "r1", seq)
	r10switch(rec, "claude", "codex")
	r10moved(rec, "codex", "claude", "r1", seq)
	rec.failed("codex", "r1", event.DeliveryNotSent)
	st := state.Replay(rec.evs)
	if o := st.SendOutcome("r1"); o != state.SendNotSent {
		t.Fatalf("r1: %q, want not_sent", o)
	}
	if id, why := forwardedAs(st, "claude", seq, ""); id != "" {
		t.Errorf("forwarding again to claude refused as %s: %q", id, why)
	}
	if sent := userSends(st, "claude", 8); len(sent) != 1 || !strings.Contains(sent[0], "did not go") {
		t.Errorf("claude's list: %q, want r1 once, as not sent", sent)
	}
	if sent := userSends(st, "codex", 8); len(sent) != 1 || !strings.Contains(sent[0], "did not go") {
		t.Errorf("codex's list: %q, want r1 once, as not sent", sent)
	}
}

// r14HeldSeat is an agent in a turn: what is sent to it is held for the end
// of the turn, and nothing of it shows until the test emits it. Its news
// can still arrive after it was killed (lingerSeat).
type r14HeldSeat struct{ *lingerSeat }

func (s r14HeldSeat) Send(id, text, mode string) (string, error) {
	s.mu.Lock()
	s.sent = append(s.sent, text)
	s.mu.Unlock()
	return "held for Codex to read when its turn ends", nil
}

// r14RelayUntil polls cond for at most two seconds and says whether it held.
func r14RelayUntil(cond func() bool) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// Finding 6. A model change waits for the agent's turn to end. The turn
// ends and the next one starts in one batch of news (Claude's Stop hook
// handing over the message held for it): the turn, idle, the held
// message's arrival, working. The arrival is recorded, and the restart
// does not call the message unconfirmed. (Review 15, finding 8: the turn's
// end does not start the agent again, which cut off the turn the message
// began; the change applies when that turn ends.)
func TestR14AnArrivalInTheBatchThatEndsATurnSurvivesAWaitingModelChange(t *testing.T) {
	old := r14HeldSeat{&lingerSeat{newFakeSeat("one")}}
	next := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	relayState := func() (s string) {
		rt.sync(func() { s = rt.st.Relays["codex"].State })
		return s
	}
	old.emit(relay.Event{Kind: relay.KindState, State: "working"})
	waitFor(t, "the agent in a turn", func() bool { return relayState() == "working" })
	userSays(t, rt, "run the tests")
	waitFor(t, "the message handed to the agent", func() bool { return len(old.sentTexts()) == 1 })
	id := lastSendID(rt)
	rt.sync(func() {
		if msg, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil || !strings.Contains(msg, "when the turn ends") {
			t.Errorf("repick: %q %v", msg, err)
		}
	})
	old.emit(
		relay.Event{Kind: relay.KindTurn, Text: "built it", DurationMS: 1000},
		relay.Event{Kind: relay.KindState, State: "idle"},
		relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true},
		relay.Event{Kind: relay.KindState, State: "working"},
	)
	outcome := func() (o state.SendOutcome) {
		rt.sync(func() { o = rt.st.SendOutcome(id) })
		return o
	}
	waitFor(t, "the arrival recorded", func() bool { return outcome() == state.SendArrived })
	rt.sync(func() {
		if rt.relays.launch("codex") != nil {
			t.Error("the change started the agent again in the turn the message began")
		}
	})
	old.emit(relay.Event{Kind: relay.KindTurn, Text: "ran the tests", DurationMS: 1000})
	g.waitEntered(t) // that turn's end starts the agent again
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	if o := outcome(); o != state.SendArrived {
		t.Errorf("%s: %q, want arrived; failures recorded: %+v", id, o, failedDeliveries(rt))
	}
	rt.sync(func() {
		if m := rt.st.Relays["codex"].Model; m != "gpt-6.2-astra" {
			t.Errorf("model after the restart: %q", m)
		}
	})
}
