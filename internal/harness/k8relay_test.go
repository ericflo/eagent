package harness

// The sixteenth review of the relay area, fifth pass (sr5).

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// sr5CommandSeat types one of the agent's own commands as relay.Seat does
// (relay's TestSR5SeatIsQuietWhileItTypesOneOfTheAgentsOwnCommands): since
// ad52f91 its sending count keeps it not quiet for the whole of Send, and
// the command's held delivered after (sendStart). Enter into a terminal
// killed meanwhile fails, and the real seat returns a TypingError for a
// command (Send: command || untrack).
type sr5CommandSeat struct {
	*fakeSeat
	entered chan string
	gate    chan struct{}
	once    sync.Once
}

func (s *sr5CommandSeat) Send(id, text, mode string) (_ string, err error) {
	first := false
	s.once.Do(func() { first = true })
	if !first {
		return s.fakeSeat.Send(id, text, mode)
	}
	s.entered <- text // pasted; typeSettle, then Enter
	<-s.gate
	if s.wasKilled() {
		return "", &relay.TypingError{Err: errors.New("can't find pane: sr5")}
	}
	s.emit(relay.Event{Kind: relay.KindInner, InnerID: "sess-new"}, relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
	return "typed the command into pi", nil
}

// SR5-1. pi is idle; the user's /new goes to it (relay_send, a passthrough
// line) and, while the seat is typing it, the user picks another effort in
// the thread's settings (or /model). heldBack asks the seat, which is quiet
// while a command is typed, so the change restarts pi at once: the restart
// kills pi under the half-typed /new. The /new is reported unconfirmed (a
// TypingError) and is never typed into the new pi, which opens the session
// /new was to leave. The pass before ca22fca held exactly this ("is being
// typed one of its own commands": typedCommand), and EVENTS.md still
// promises the change waits while a send is "still being typed".
func TestSR5AChangeWhileACommandIsTypedRestartsTheAgentUnderIt(t *testing.T) {
	seat := &sr5CommandSeat{fakeSeat: newFakeSeat("…"), entered: make(chan string, 4), gate: make(chan struct{})}
	next := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
	rt := sr3PiRuntime(t)
	sent := make(chan string, 1)
	go func() {
		out, _ := rt.relayTool("relay_send", argStr(map[string]any{"text": "/new"}), argNum(nil), argList(nil), false)
		sent <- out
	}()
	recv(t, seat.entered, "/new being typed")
	id := lastSendID(rt)
	var msg string
	var err error
	rt.sync(func() { msg, err = rt.repickRelay("pi", relayPick{"", "high"}) })
	if err != nil {
		t.Fatalf("repick: %v", err)
	}
	restarting := false
	rt.sync(func() { restarting = rt.relays.launch("pi") != nil })
	t.Logf("change asked while /new is typed: %q (restart under way: %v)", msg, restarting)
	if restarting {
		t.Errorf("the change restarted pi while its /new was being typed (%q): it should wait until the command is typed and acted on", msg)
		waitFor(t, "the old pi killed by the restart", seat.wasKilled)
	}
	close(seat.gate)
	t.Logf("send: %q", recv(t, sent, "relay_send"))
	if restarting {
		g.waitEntered(t)
		g.release <- struct{}{}
	}
	waitFor(t, "the /new's outcome", func() bool { return len(deliveredFor(rt, id)) > 0 })
	var outcome state.SendOutcome
	rt.sync(func() { outcome = rt.st.SendOutcome(id) })
	typedAgain := false
	for _, s := range next.sentTexts() {
		typedAgain = typedAgain || strings.TrimSpace(s) == "/new"
	}
	t.Logf("/new: %q %+v; typed into the new pi: %v", outcome, deliveredFor(rt, id), typedAgain)
	if outcome != state.SendArrived && !typedAgain {
		t.Errorf("the user's /new never ran: it is %q and was not typed into the pi the change started", outcome)
	}
}

// SR5-2. Just after pi has started (the launch is over, the agent not yet
// at its prompt: the seat says starting), the user picks another effort.
// Nothing was sent and pi runs none of its own commands, but the change is
// told that pi "is not between turns yet (something sent to it is on its
// way, or it is acting on one of its own commands)": the seat is not quiet
// while starting, and repickRelay explains every hold that is not a dialog
// or a recorded turn that way.
func TestSR5AChangeRightAfterAStartIsToldSomethingWasSent(t *testing.T) {
	seat := newFakeSeat("…")
	useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := sr3PiRuntime(t)
	seat.setState("starting")
	var msg string
	var err error
	var sends int
	rt.sync(func() {
		rt.st.Relays["pi"].State = "starting"
		sends = rt.st.Relays["pi"].Sends
		msg, err = rt.repickRelay("pi", relayPick{"", "high"})
	})
	if err != nil {
		t.Fatalf("repick: %v", err)
	}
	t.Logf("sends recorded: %d; change: %q", sends, msg)
	if sends == 0 && strings.Contains(msg, "something sent to it is on its way") {
		t.Errorf("nothing was sent to pi, which is starting, but the change says %q", msg)
	}
	if !strings.Contains(msg, "still starting") {
		t.Errorf("the change says %q; want that pi is still starting", msg)
	}
}

// An agent that exits (the user's own /exit among the ways) while a change
// waits for its turn is not started again; the change is what its next
// start runs, and a later restart starts it once, with the change.
func TestK8AWaitingChangeIsTheNextStartOfAnAgentThatExited(t *testing.T) {
	seat := newFakeSeat("one")
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two"), newFakeSeat("three")}, nil)
	rt := newLaunchRuntime(t)
	seat.emit(relay.Event{Kind: relay.KindState, State: "working"})
	k1Relay(t, rt, "the agent in a turn", func(rel *state.Relay) bool { return rel.State == "working" })
	if msg := k1Repick(t, rt); !strings.Contains(msg, "when the turn ends") {
		t.Fatalf("repick in a turn: %q", msg)
	}
	seat.emit(relay.Event{Kind: relay.KindState, State: "exited", Detail: "$ "})
	k1Relay(t, rt, "exited recorded", func(rel *state.Relay) bool { return rel.State == "exited" })
	if k1Restarting(rt) { // decided in the step that recorded the exit
		t.Fatal("the agent that exited was started again by the waiting change")
	}
	rt.sync(func() {
		if route := rt.relayRoutes()["codex"]; route.Model != "gpt-6.2-astra" {
			t.Errorf("after the exit: live settings %+v; want gpt-6.2-astra as what its next start runs", route)
		}
	})
	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, restarted, "restart")
	waitRelayStarts(t, rt)
	k1Relay(t, rt, "the new model", func(rel *state.Relay) bool { return rel.Model == "gpt-6.2-astra" })
	if k1Restarting(rt) { // a chained start begins in the step that recorded this one
		t.Error("the restart was followed by a second start")
	}
}
