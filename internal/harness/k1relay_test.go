package harness

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// k1QuietSeat takes every message and shows nothing of it until the test
// emits it; its news can still arrive after it was killed (lingerSeat).
type k1QuietSeat struct{ *lingerSeat }

func newK1QuietSeat() k1QuietSeat { return k1QuietSeat{&lingerSeat{newFakeSeat("one")}} }

func (s k1QuietSeat) Send(id, text, mode string) (string, error) {
	s.mu.Lock()
	s.sent = append(s.sent, text)
	s.modes = append(s.modes, mode)
	s.mu.Unlock()
	return "typed into Codex", nil
}

// k1QueuesIdle waits until no send is queued or being typed.
func k1QueuesIdle(t *testing.T, rt *Runtime) {
	t.Helper()
	waitFor(t, "the send queues to finish", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		return len(rt.relays.queues) == 0
	})
}

// k1Stop stops the relay as relay_control stop does.
func k1Stop(t *testing.T, rt *Runtime) {
	t.Helper()
	if out := relayToolNamed(t, rt, "relay_control", map[string]any{"action": "stop"}); !strings.Contains(out, "Stopped") {
		t.Fatalf("stop: %q", out)
	}
}

// k1Typed has the user's message typed into seat, its arrival not shown,
// and returns its send id once the queue has finished typing it.
func k1Typed(t *testing.T, rt *Runtime, seat k1QuietSeat) string {
	t.Helper()
	userSays(t, rt, "run the tests")
	waitFor(t, "the message typed", func() bool { return len(seat.sentTexts()) == 1 })
	k1QueuesIdle(t, rt)
	return lastSendID(rt)
}

// k1StoppedOnce checks that send id has one outcome: unconfirmed, as its
// agent was stopped.
func k1StoppedOnce(t *testing.T, rt *Runtime, id string) {
	t.Helper()
	got := deliveredFor(rt, id)
	if len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed || !strings.Contains(got[0].Detail, "was stopped") {
		t.Fatalf("%s after the stop: %+v; want one unconfirmed outcome saying the agent was stopped", id, got)
	}
}

// A5. A message typed into Codex, its arrival not yet shown, when Codex is
// stopped: nothing will confirm it now, so the stop reports it unconfirmed,
// once; a fresh Codex started under the name adds nothing to it.
func TestK1ASendTypedIntoAStoppedAgentIsSettledAtTheStop(t *testing.T) {
	seat := newK1QuietSeat()
	fresh := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{seat, fresh}, nil)
	rt := newLaunchRuntime(t)
	id := k1Typed(t, rt, seat)
	k1Stop(t, rt)
	k1StoppedOnce(t, rt, id)
	started := make(chan string, 1)
	go func() {
		out, _ := rt.relayTool("relay_start", argStr(map[string]any{"profile": "codex"}), argNum(nil), argList(nil), false)
		started <- out
	}()
	g.waitEntered(t)
	g.release <- struct{}{}
	if out := recv(t, started, "relay_start"); !strings.Contains(out, "Started.") {
		t.Fatalf("relay_start: %q", out)
	}
	if got := deliveredFor(rt, id); len(got) != 1 {
		t.Fatalf("%s after a fresh start under the name: %d outcomes %+v, want 1", id, len(got), got)
	}
	var o state.SendOutcome
	rt.sync(func() { o = rt.st.SendOutcome(id) })
	if o != state.SendUnconfirmed {
		t.Errorf("%s: %q, want unconfirmed", id, o)
	}
	if s := fresh.sentTexts(); len(s) != 0 {
		t.Errorf("typed into the fresh agent: %q", s)
	}
}

// A5, a stop while a restart is under way: the stopped agent's message is
// settled once, where the restart the stop cut short ends; an arrival the
// stopped agent still showed before then is its outcome instead, alone.
func TestK1ASendTypedIntoAnAgentStoppedWhileItRestartsIsSettledOnce(t *testing.T) {
	for _, arrives := range []bool{false, true} {
		seat := newK1QuietSeat()
		g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
		rt := newLaunchRuntime(t)
		id := k1Typed(t, rt, seat)
		rt.sync(func() {
			if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
				t.Errorf("repick: %v", err)
			}
		})
		g.waitEntered(t)
		k1Stop(t, rt)
		if got := deliveredFor(rt, id); len(got) != 0 {
			t.Fatalf("arrives %v: %s settled while the restart still takes the old agent's news: %+v", arrives, id, got)
		}
		if arrives {
			seat.emit(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
			waitFor(t, "the arrival recorded", func() bool { return len(deliveredFor(rt, id)) == 1 })
		}
		g.release <- struct{}{}
		waitRelayStarts(t, rt)
		if !arrives {
			k1StoppedOnce(t, rt, id)
			continue
		}
		if got := deliveredFor(rt, id); len(got) != 1 || !got[0].OK {
			t.Fatalf("%s: %+v, want its arrival alone", id, got)
		}
	}
}

// A17. A message held for the end of Codex's turn when Codex is stopped:
// a fresh Codex under the name clears the spool it waited in, and the
// record does not leave it held for ever. It is settled with the stop,
// once, and the fresh start is not typed it.
func TestK1AHeldSendOfAStoppedAgentIsSettledBeforeAFreshStart(t *testing.T) {
	seat := newK1QuietSeat()
	fresh := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{seat, fresh}, nil)
	rt := newLaunchRuntime(t)
	seat.emit(relay.Event{Kind: relay.KindState, State: "working"})
	waitFor(t, "the agent in a turn", func() bool {
		var s string
		rt.sync(func() { s = rt.st.Relays["codex"].State })
		return s == "working"
	})
	if out, isErr := rt.relayTool("relay_send", argStr(map[string]any{"text": "then run the linter", "mode": "after_turn"}), argNum(nil), argList(nil), false); isErr {
		t.Fatalf("relay_send: %s", out)
	}
	id := lastSendID(rt)
	var o state.SendOutcome
	rt.sync(func() { o = rt.st.SendOutcome(id) })
	if o != state.SendHeld {
		t.Fatalf("%s before the stop: %q, want held", id, o)
	}
	k1Stop(t, rt)
	started := make(chan string, 1)
	go func() {
		out, _ := rt.relayTool("relay_start", argStr(map[string]any{"profile": "codex"}), argNum(nil), argList(nil), false)
		started <- out
	}()
	g.waitEntered(t)
	g.release <- struct{}{}
	if out := recv(t, started, "relay_start"); !strings.Contains(out, "Started.") {
		t.Fatalf("relay_start: %q", out)
	}
	k1StoppedOnce(t, rt, id)
	rt.sync(func() { o = rt.st.SendOutcome(id) })
	if o != state.SendUnconfirmed {
		t.Errorf("%s after the fresh start: %q, want unconfirmed", id, o)
	}
	if s := fresh.sentTexts(); len(s) != 0 {
		t.Errorf("typed into the fresh agent: %q", s)
	}
}

// A5, Stop session: it stops the agents as relay_control stop does, and
// settles what they were typed the same way.
func TestK1ASendTypedIntoAnAgentStopSessionStopsIsSettledOnce(t *testing.T) {
	seat := newK1QuietSeat()
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	id := k1Typed(t, rt, seat)
	rt.sync(func() { rt.stopSession("stopped from the app") })
	k1StoppedOnce(t, rt, id)
}
