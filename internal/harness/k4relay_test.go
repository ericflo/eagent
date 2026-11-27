package harness

// The coordinator's review tests (sr_relay_test.go), copied.

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// SR-1. The user types one of the agent's own commands (/clear) into an
// idle Codex and, while it is being typed, changes the model. The change
// waits for the command ("has a message on its way ... when the turn that
// message starts ends"). The seat confirms a command as soon as it is typed
// (relay.Seat.Send emits it delivered) and the command starts no turn, so
// nothing looks at the waiting change again: the agent sits idle and the
// change waits for good. On main the change started the agent again at once.
func TestSRRelayCommandOnItsWayLeavesAModelChangeWaitingForATurnThatNeverComes(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, seat, "/clear", "queue")
	msg := k1Repick(t, rt)
	if k1Restarting(rt) {
		// A command starts no turn and holds nothing back: the change
		// starts the agent again at once, as on main.
		seat.end()
		g.release <- struct{}{}
		waitRelayStarts(t, rt)
		return
	}
	t.Logf("told: %q", msg)
	// The command's confirmation, as the real seat gives it right after
	// typing; no turn follows a command.
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true}, relay.Event{Kind: relay.KindActivity, Text: "…"})
	rt.sync(func() {})
	var waiting bool
	var state string
	rt.sync(func() {
		_, waiting = rt.relayRepick["codex"]
		state = rt.st.Relays["codex"].State
	})
	if !k1Restarting(rt) {
		t.Errorf("Codex is %s with nothing on its way and the change still waiting (%v): it never applies, though the model was told %q", state, waiting, msg)
		return
	}
	seat.end()
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
}

// A review's case: eagent typed words of its own into Codex (relay_send),
// their arrival not shown, and then switched to Claude. The brief quotes
// only the user's sends, so nothing carries eagent's: with Codex stopped,
// the send is reported unconfirmed, once, as at a stop. The user's send
// stays as before (the brief quotes it).
func TestK4AnEagentSendToTheAgentASwitchReplacesIsSettled(t *testing.T) {
	seat := newK1QuietSeat()
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	if out, isErr := rt.relayTool("relay_send", argStr(map[string]any{"text": "run the linter"}), argNum(nil), argList(nil), false); isErr {
		t.Fatalf("relay_send: %s", out)
	}
	id := lastSendID(rt)
	k1QueuesIdle(t, rt)
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, switched, "switch")
	waitRelayStarts(t, rt)
	if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed {
		t.Fatalf("%s: %+v; want its one outcome, unconfirmed", id, got)
	}
}

// SR-2. A model change restarts Codex beside the loop; the user quits
// eagent (/quit) while the new Codex comes up. eagent's agents outlive it
// (closeRelays only stops watching), and on main the restart ran on the
// loop, so the quit came after it and left the new Codex running. Here the
// start is given up (abandonLaunch) and kills the new Codex: the old one
// was stopped by the restart, so no agent runs after the quit.
func TestSRRelayQuitDuringAModelChangeLeavesNoAgentRunning(t *testing.T) {
	one, two := newFakeSeat("one"), newFakeSeat("two")
	rt, g, done := r10Session(t, one, two)
	rt.sync(func() {
		if msg, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil || !strings.Contains(msg, "starting again") {
			t.Errorf("repick: %q %v", msg, err)
		}
	})
	g.waitEntered(t)
	r10Quit(t, rt, g, 1, done)
	if !one.wasKilled() {
		t.Fatal("the old Codex was not stopped by the restart")
	}
	if two.wasKilled() {
		t.Error("the quit killed the Codex the model change brought up: no agent is left running, where main left the restarted one running")
	}
	// Recorded as started, for a resume to reattach to.
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	if rel := state.Replay(evs).Relays["codex"]; rel == nil || !rel.Active() || rel.Model != "gpt-6.2-astra" {
		t.Errorf("after the quit the record has %+v; want the restarted Codex, running gpt-6.2-astra", rel)
	}
}
