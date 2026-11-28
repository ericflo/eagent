package harness

// The sixteenth review of the relay area, seventh pass (sr7).

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// sr7ExitWithAWaitingChange: Codex is in a turn, the user picks
// gpt-6.2-astra (it waits for the turn's end), and Codex exits (the user's
// own /exit). The change is now what its next start runs (relayNext).
func sr7ExitWithAWaitingChange(t *testing.T) (*Runtime, *gatedLaunches, relayPick) {
	t.Helper()
	seat := newFakeSeat("one")
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two"), newFakeSeat("three")}, nil)
	rt := newLaunchRuntime(t)
	var was relayPick
	rt.sync(func() {
		rel := rt.st.Relays["codex"]
		was = relayPick{rel.Model, rel.Effort}
	})
	seat.emit(relay.Event{Kind: relay.KindState, State: "working"})
	k1Relay(t, rt, "the agent in a turn", func(rel *state.Relay) bool { return rel.State == "working" })
	if msg := k1Repick(t, rt); !strings.Contains(msg, "when the turn ends") {
		t.Fatalf("repick in a turn: %q", msg)
	}
	seat.emit(relay.Event{Kind: relay.KindState, State: "exited", Detail: "$ "})
	k1Relay(t, rt, "exited recorded", func(rel *state.Relay) bool { return rel.State == "exited" })
	if k1Restarting(rt) {
		t.Fatal("the agent that exited was started again by the waiting change")
	}
	return rt, g, was
}

// SR7-1a. After the exit the user takes the change back: they pick the
// model Codex ran (in the thread's settings, or relay_control model). They
// are told "Codex already runs that.", but the change stays as what the
// next start runs (relayNext is not cleared), the settings snap back to
// gpt-6.2-astra, and the next restart runs it. On main the waiting change
// stayed in relayRepick, which picking the running model cleared, and the
// restart ran the model the user picked last.
func TestSR7PickingBackTheModelAfterAnExitIsKept(t *testing.T) {
	rt, g, was := sr7ExitWithAWaitingChange(t)
	var msg string
	var err error
	rt.sync(func() { msg, err = rt.repickRelay("codex", was) })
	if err != nil {
		t.Fatalf("repick back: %v", err)
	}
	rt.sync(func() {
		if route := rt.relayRoutes()["codex"]; route.Model != was.Model {
			t.Errorf("told %q; the live settings now show %+v, not the model the user picked back (%q)", msg, route, was.Model)
		}
	})
	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, restarted, "restart")
	waitRelayStarts(t, rt)
	rt.sync(func() {
		if rel := rt.st.Relays["codex"]; rel.Model != was.Model {
			t.Errorf("told %q, then restarted: it runs %q; want %q, the model the user picked last", msg, rel.Model, was.Model)
		}
	})
}

// SR7-1b. After the exit the user asks only for another effort (/model
// high, or relay_control model with an effort). The change is asked now of
// an agent that exited, so it starts it again, but from the model the
// record shows, not the one still to run: the user's model change is
// dropped. On main the waiting change was the base the effort joined
// (relay_control model read relayRepick, where the change still was).
func TestSR7AnEffortAfterAnExitDropsTheWaitingModel(t *testing.T) {
	rt, g, _ := sr7ExitWithAWaitingChange(t)
	changed := relayToolAsync(rt, map[string]any{"action": "model", "effort": "high"})
	msg := recv(t, changed, "relay_control model")
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	rt.sync(func() {
		if rel := rt.st.Relays["codex"]; rel.Model != "gpt-6.2-astra" || rel.Effort != "high" {
			t.Errorf("told %q; it runs model %q at %q; want gpt-6.2-astra at high (the model change still to run, and the effort asked now)", msg, rel.Model, rel.Effort)
		}
	})
}

// SR7-1's last part: a stop after the exit drops the change kept for the
// agent's next start (it was for the agent stopped): a later restart of the
// stopped relay runs what it runs on the record.
func TestK10AStopAfterAnExitDropsTheKeptChange(t *testing.T) {
	rt, g, was := sr7ExitWithAWaitingChange(t)
	if out := relayToolNamed(t, rt, "relay_control", map[string]any{"action": "stop"}); !strings.Contains(out, "Stopped") {
		t.Fatalf("stop: %s", out)
	}
	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, restarted, "restart")
	waitRelayStarts(t, rt)
	rt.sync(func() {
		if rel := rt.st.Relays["codex"]; rel.Model != was.Model {
			t.Errorf("restarted after the stop: it runs %q; want %q, not the change kept for the agent stopped", rel.Model, was.Model)
		}
	})
}

// A model change restarts Codex; the old seat's last look reads the user's
// message arriving; the user quits in that moment (the agents outlive the
// session, for a resume). The restart settles the send as unconfirmed, and
// the arrival, when the loop still takes it, is the newer word. Either way
// the send has an outcome on the record, and never one that says it was
// not sent.
func TestSR7AQuitDuringARestartLeavesTheSendWithAnOutcome(t *testing.T) {
	seat := newK2FeedSeat(t)
	rt, g, done := r10Session(t, seat, newFakeSeat("two"))
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	rt.sync(func() {
		if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("restart: %v", err)
		}
	})
	seat.waitGone(t) // the restart stopped the old Codex; its seat drains
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true}) // its last look
	seat.end()
	g.waitEntered(t)
	g.release <- struct{}{}
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
	if len(got) == 0 {
		t.Fatalf("%s has no outcome on the record", id)
	}
	if last := got[len(got)-1]; !last.OK && last.Outcome != relay.OutcomeUnconfirmed {
		t.Errorf("%s, read arriving by the old seat as the user quit: %+v; want it to end arrived or unconfirmed", id, got)
	}
}

// SR7-2 (the known limit "a send typed into an agent that is then stopped
// stays unresolved"). The user stops Codex from the phone (/stop-relay,
// whose relay_control stop runs off any turn) and, a moment later, presses
// Stop session. relay_control stop settles what Codex was typed once its
// seat has said all it saw; Stop session skips Codex (already ended), the
// loop closes, the stop's wait gives up at the close and its settle step
// never runs. The message typed into Codex has no outcome before
// session.end, and a resume never settles it (an ended relay is not
// reattached).
func TestSR7StopSessionDuringAStopLeavesTheSendUnresolved(t *testing.T) {
	seat := newK2FeedSeat(t)
	rt, _, done := r10Session(t, seat)
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	stopped := relayToolAsync(rt, map[string]any{"action": "stop", "reason": "the user stopped it"})
	seat.waitGone(t) // killed; its watcher has not ended yet
	rt.sync(func() { rt.stopSession("the user stopped the session") })
	select {
	case <-rt.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop never closed")
	}
	seat.end() // its watcher ends now, with nothing more to say
	recv(t, stopped, "relay_control stop")
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	var outcomes []string
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id {
			outcomes = append(outcomes, d.Outcome)
		}
	}
	if len(outcomes) != 1 {
		t.Errorf("%s, typed into Codex before it was stopped: outcomes %v; want one (unconfirmed: the agent was stopped) before session.end", id, outcomes)
	}
}
