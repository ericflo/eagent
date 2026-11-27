package harness

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// k1QuietSeat takes every message and shows nothing of it until the test
// emits it; its news can still arrive after it was killed (lingerSeat).
type k1QuietSeat struct{ *lingerSeat }

func newK1QuietSeat() k1QuietSeat { return k1QuietSeat{&lingerSeat{newFakeSeat("one")}} }

func (s k1QuietSeat) Send(id, text, mode string) (_ string, err error) {
	defer s.sendStart(id)(&err)
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
			if arrives {
				// Said before the kill ends the old seat's events, read once
				// the restart has taken the seat out of use.
				seat.emit(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
			}
			// The restart a model change makes (a message on its way now
			// holds a change asked back, item 3).
			if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
				t.Errorf("repick: %v", err)
			}
		})
		g.waitEntered(t)
		k1Stop(t, rt)
		if got := deliveredFor(rt, id); !arrives && len(got) != 0 {
			t.Fatalf("%s settled while the restart is still under way: %+v", id, got)
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

// A18. Codex does the work and reports; a switch hands it to Claude with a
// brief, and Claude has finished no turn when eagent restarts. The next
// switch's brief, written by the restarted eagent from the record, hands
// over from Codex, with Claude in between (told, or not when its brief
// could not be typed), as the eagent that made the switch would have.
func TestK1ABriefAfterARestartHandsOverFromTheAgentThatDidTheWork(t *testing.T) {
	for _, typed := range []bool{true, false} {
		var claude relaySeat = newK1QuietSeat()
		if !typed {
			failing := newFakeSeat("two")
			failing.setSendErr(errors.New("the agent is still starting; check relay_screen"))
			claude = failing
		}
		g := useGatedLaunches(t, []relaySeat{newFakeSeat("parsed the config files"), claude}, nil)
		rt := newLaunchRuntime(t)
		userSays(t, rt, "build the config parser")
		waitFor(t, "Codex's report", func() bool {
			var n int
			rt.sync(func() { n = rt.st.Relays["codex"].Turns })
			return n == 1
		})
		switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
		g.waitEntered(t)
		g.release <- struct{}{}
		if out := recv(t, switched, "switch"); !strings.Contains(out, "claude") {
			t.Fatalf("switch: %q", out)
		}
		var live, restarted string
		rt.sync(func() {
			live = rt.writeBrief(rt.handoverFor(rt.st.Relays["claude"]), nil)
			// What an eagent resumed on this record has: the record alone.
			fresh := &Runtime{st: state.Replay(slices.Clone(rt.st.Events))}
			restarted = fresh.writeBrief(fresh.handoverFor(fresh.st.Relays["claude"]), nil)
		})
		in := "was told the work in between"
		if !typed {
			in = "was started to take over in between"
		}
		if !strings.HasPrefix(live, "You are taking over this work from Codex") || !strings.Contains(live, in) || !strings.Contains(live, "parsed the config files") {
			t.Fatalf("typed %v: the brief before a restart:\n%s", typed, live)
		}
		if restarted != live {
			t.Errorf("typed %v: the brief after a restart:\n%s\nwant, as before it:\n%s", typed, restarted, live)
		}
	}
}

// k1HeldSeat is an agent whose terminal the test can hold blocked (a
// dialog it reports, or quiet in a turn): Snapshot says what is set, the
// dialog's title among it while one holds it, as a real seat's does.
type k1HeldSeat struct {
	*lingerSeat
	smu           sync.Mutex
	state, dialog string
}

func newK1HeldSeat() *k1HeldSeat {
	return &k1HeldSeat{lingerSeat: &lingerSeat{newFakeSeat("one")}, state: "idle"}
}

func (s *k1HeldSeat) set(state string) {
	s.smu.Lock()
	s.state, s.dialog = state, ""
	s.smu.Unlock()
}

// setDialog holds the seat blocked on a dialog the agent reports.
func (s *k1HeldSeat) setDialog(title string) {
	s.smu.Lock()
	s.state, s.dialog = "blocked", title
	s.smu.Unlock()
}

func (s *k1HeldSeat) Snapshot() relay.Status {
	s.fakeSeat.mu.Lock()
	unsaid := len(s.fakeSeat.unsaid)
	s.fakeSeat.mu.Unlock()
	s.smu.Lock()
	defer s.smu.Unlock()
	return relay.Status{State: s.state, Since: time.Now(), Dialog: s.dialog, Quiet: s.state == "idle" && s.dialog == "" && unsaid == 0}
}

const (
	k1DialogTitle = "Allow rm -rf build?"
	k1Dialog      = "Codex is waiting on a dialog: Allow rm -rf build?; see relay_screen and answer it with relay_control keys"
)

// k1DialogUp is the blocked state a seat says as a dialog the agent
// reports comes up.
var k1DialogUp = relay.Event{Kind: relay.KindState, State: "blocked", Detail: k1Dialog, Dialog: k1DialogTitle}

// k1Relay waits until the Codex relay on the record meets cond.
func k1Relay(t *testing.T, rt *Runtime, what string, cond func(*state.Relay) bool) {
	t.Helper()
	waitFor(t, what, func() (ok bool) {
		rt.sync(func() { ok = cond(rt.st.Relays["codex"]) })
		return ok
	})
}

// k1Repick asks for another model and returns what it said.
func k1Repick(t *testing.T, rt *Runtime) string {
	t.Helper()
	var msg string
	var err error
	rt.sync(func() { msg, err = rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}) })
	if err != nil {
		t.Fatalf("repick: %v", err)
	}
	return msg
}

func k1Restarting(rt *Runtime) (yes bool) {
	rt.sync(func() { yes = rt.relays.launch("codex") != nil })
	return yes
}

// k1AppliedOnceIdle has the dialog end (the seat says idle) and checks that
// the waiting change starts the agent again then, and only then.
func k1AppliedOnceIdle(t *testing.T, rt *Runtime, g *gatedLaunches, seat *k1HeldSeat) {
	t.Helper()
	if k1Restarting(rt) {
		t.Fatal("the agent was started again under its dialog")
	}
	k1Applied(t, rt, g, seat)
}

// k1Applied has the seat say idle and checks the waiting change has
// started the agent again with the new model.
func k1Applied(t *testing.T, rt *Runtime, g *gatedLaunches, seat *k1HeldSeat) {
	t.Helper()
	seat.set("idle")
	seat.emit(relay.Event{Kind: relay.KindState, State: "idle"})
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	k1Relay(t, rt, "the new model", func(rel *state.Relay) bool { return rel.Model == "gpt-6.2-astra" })
}

// A11. A model change waits for Codex's turn to end. A dialog the agent
// reports comes up; it still writes progress and ends its turn under it.
// The record stays blocked through the progress, the turn's end does not
// start the agent again under its dialog, and the change applies once the
// seat says the dialog is over.
func TestK1AWaitingModelChangeIsNotAppliedUnderADialog(t *testing.T) {
	seat := newK1HeldSeat()
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	seat.set("working")
	seat.emit(relay.Event{Kind: relay.KindState, State: "working"})
	k1Relay(t, rt, "the agent in a turn", func(rel *state.Relay) bool { return rel.State == "working" })
	if msg := k1Repick(t, rt); !strings.Contains(msg, "when the turn ends") {
		t.Fatalf("repick in a turn: %q", msg)
	}
	seat.setDialog(k1DialogTitle)
	seat.emit(k1DialogUp, relay.Event{Kind: relay.KindProgress, Text: "Checking the build."})
	k1Relay(t, rt, "the progress recorded", func(rel *state.Relay) bool { return len(rel.Recent) == 1 })
	rt.sync(func() {
		if st := rt.st.Relays["codex"].State; st != "blocked" {
			t.Errorf("the record says %s under the dialog, want blocked", st)
		}
	})
	seat.emit(relay.Event{Kind: relay.KindTurn, Text: "Done."})
	k1Relay(t, rt, "the turn recorded", func(rel *state.Relay) bool { return rel.Turns == 1 })
	k1AppliedOnceIdle(t, rt, g, seat)
}

// A11, a change asked while the dialog is up (the earlier fix's case: the
// record kept blocked through progress, and the change started the agent
// again at once, mid-turn, under its dialog). It waits, also when the
// dialog came up between turns, and applies once the dialog is over.
func TestK1AModelChangeAskedUnderADialogWaits(t *testing.T) {
	for _, midTurn := range []bool{true, false} {
		seat := newK1HeldSeat()
		g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
		rt := newLaunchRuntime(t)
		if midTurn {
			seat.emit(relay.Event{Kind: relay.KindState, State: "working"})
		}
		seat.setDialog(k1DialogTitle)
		seat.emit(k1DialogUp)
		if midTurn {
			seat.emit(relay.Event{Kind: relay.KindProgress, Text: "Checking the build."})
		}
		k1Relay(t, rt, "the dialog recorded", func(rel *state.Relay) bool {
			if midTurn {
				return len(rel.Recent) == 1
			}
			return rel.State == "blocked"
		})
		if msg := k1Repick(t, rt); k1Restarting(rt) || strings.Contains(msg, "is starting again") {
			t.Fatalf("mid-turn %v: the change started the agent under its dialog: %q", midTurn, msg)
		}
		if midTurn {
			seat.emit(relay.Event{Kind: relay.KindTurn, Text: "Done."})
			k1Relay(t, rt, "the turn recorded", func(rel *state.Relay) bool { return rel.Turns == 1 })
		}
		k1AppliedOnceIdle(t, rt, g, seat)
	}
}

// A11, its bound: an agent on autopilot blocked for going quiet in a turn
// ends that turn: the autopilot idle check is armed at the turn as before.
// (A change that waits through such a block applies at the block itself,
// as one asked then does: TestK8AWaitingChangeAppliesWhenTheAgentIsStuckOrExits.)
func TestK1AQuietAgentsTurnArmsTheIdleCheck(t *testing.T) {
	seat := newK1HeldSeat()
	useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		rt.append(event.New(event.RelayAutopilot, event.ActorHarness, event.RelayAutopilotData{Name: "codex", Instruction: "keep going", Decide: true}))
	})
	seat.set("working")
	seat.emit(relay.Event{Kind: relay.KindState, State: "working"})
	k1Relay(t, rt, "the agent in a turn", func(rel *state.Relay) bool { return rel.State == "working" })
	seat.set("blocked")
	seat.emit(relay.Event{Kind: relay.KindState, State: "blocked", Detail: "no transcript activity for 10m; the screen ends:\n…"}, relay.Event{Kind: relay.KindTurn, Text: "Built it."})
	k1Relay(t, rt, "the turn recorded", func(rel *state.Relay) bool { return rel.Turns == 1 && rel.State == "idle" })
	rt.sync(func() {
		if rt.autopilotChecks["codex"] == nil {
			t.Error("the autopilot idle check was not armed at the quiet agent's turn")
		}
	})
}

// A5, a switch whose new agent cannot start: the old agent was stopped for
// it and its relay ends, with no brief to carry what it was typed. That
// send is settled once, as at a stop.
func TestK1ASendTypedIntoAnAgentASwitchStoppedInVainIsSettledOnce(t *testing.T) {
	seat := newK1QuietSeat()
	g := useGatedLaunches(t, []relaySeat{seat, nil}, []error{nil, errors.New("claude is not installed (not on PATH)")})
	rt := newLaunchRuntime(t)
	id := k1Typed(t, rt, seat)
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	if out := recv(t, switched, "switch"); !strings.Contains(out, "Could not switch") {
		t.Fatalf("switch: %q", out)
	}
	k1StoppedOnce(t, rt, id)
}

// A5, Stop session: it stops the agents as relay_control stop does, and
// settles what they were typed the same way.
func TestK1ASendTypedIntoAnAgentStopSessionStopsIsSettledOnce(t *testing.T) {
	seat := newK1QuietSeat()
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	id := k1Typed(t, rt, seat)
	rt.sync(func() { rt.stopSession("stopped from the app") })
	// Settled once the stopped seat has drained (SR4-3), as at a stop.
	waitFor(t, "the send settled", func() bool { return len(deliveredFor(rt, id)) > 0 })
	k1StoppedOnce(t, rt, id)
}
