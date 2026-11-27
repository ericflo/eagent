package harness

// The sixteenth review of the relay area (starts, restarts, switches,
// stops, sends, waiting model changes, live settings).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// SR2-3. eagent switches Codex to Claude; while Claude starts, it sends the
// agent words of its own (relay_send, no relay named). Codex is still the
// active relay on the record and its name is held by the switch
// (launching), so the send is held and goes to Claude after the brief
// (relay.moved). The tool's answer says the opposite: "Codex is starting;
// the message is typed in once it is up", though Codex is being stopped.
func TestSR2ASendDuringASwitchSaysWhichAgentGetsIt(t *testing.T) {
	g := useGatedLaunches(t, []relaySeat{newK1QuietSeat(), newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	out, isErr := rt.relayTool("relay_send", argStr(map[string]any{"text": "also run the tests"}), argNum(nil), argList(nil), false)
	g.release <- struct{}{}
	recv(t, switched, "switch")
	waitRelayStarts(t, rt)
	if isErr || strings.Contains(out, "Codex is starting") || !strings.Contains(out, "Claude") {
		t.Errorf("relay_send during the switch to Claude answered %q; the words go to Claude, and Codex is being stopped", out)
	}
}

// SR2-2. The restart half of the known limit "a held message still in the
// spool when its agent exits or is restarted ... is reported unconfirmed"
// was fixed (TestK3AHeldMessageStillInTheSpoolAtARestartIsNotSent): the
// launch takes it back (relay.Unspool) and it is not_sent, and may be
// forwarded again. Its sibling, a stop (relay_control stop, /stop-relay),
// was not: the user's message held for the end of Claude's turn still waits
// in the spool, unclaimed, when Claude is stopped (Kill does not sweep the
// spool); settleExited reports it unconfirmed ("whether it arrived cannot
// be confirmed"), the phone keeps "sent" with no Retry, and relay_forward
// refuses to send it to the agent when it is restarted, though nothing of
// it ever reached the agent.
func TestSR2AHeldMessageStillInTheSpoolAtAStopIsNotSent(t *testing.T) {
	seat := newK2FeedSeat(t)
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := k3ClaudeRuntime(t)
	var seq int64
	rt.sync(func() {
		d := event.UserMessageData{Text: "then run the linter", Delivery: "after_turn"}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		seq = ev.Seq
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("not routed: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
	waitFor(t, "the message handed over", func() bool { return len(seat.sentTexts()) == 1 })
	k1QueuesIdle(t, rt)
	id := lastSendID(rt)
	spool := filepath.Join(rt.sess.Path, "relays", "claude", "after_turn")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(spool, fmt.Sprintf("%d-%s.txt", time.Now().UnixNano(), id))
	if err := os.WriteFile(file, []byte("then run the linter"), 0o644); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan string, 1)
	go func() {
		out, _ := rt.relayTool("relay_control", argStr(map[string]any{"action": "stop"}), argNum(nil), argList(nil), false)
		stopped <- out
	}()
	seat.waitGone(t)
	seat.end()
	recv(t, stopped, "stop")
	// (The review's check that the stop left the file in the spool showed
	// the fault; now it is taken back, so no hook hands it over later.)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the spool file is still there after the stop (%v): a hook could hand it to the next agent", err)
	}
	if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
		t.Errorf("%s, still unclaimed in the spool when Claude was stopped: %+v; want its one outcome, not_sent", id, got)
	}
	var prev, why string
	rt.sync(func() { prev, why = forwardedAs(rt.st, "claude", seq, "") })
	if prev != "" {
		t.Errorf("forwarding the message to Claude again (after relay_control restart) is refused as %s: %q", prev, why)
	}
}

// SR2-1. Two agent starts are under way when the session ends: a model
// change restarting Codex (from the phone's settings) and a relay_start of
// Claude. Since f3c9c86 a start the end overtakes is recorded by
// abandonLaunch, which calls finishLaunch off the loop goroutine (the loop
// has ended). Each start's goroutine does so on its own, so the two write
// the session's state (r.st: its event list, its relay map) at once, with
// nothing ordering them: under -race this test fails, and without it the
// runtime can abort with "concurrent map writes" before session.end and the
// not_sent outcomes finish writes after them.
func TestSR2TwoStartsTheEndOvertakesAreRecordedOneAtATime(t *testing.T) {
	one, two, three := newFakeSeat("one"), newFakeSeat("two"), newFakeSeat("three")
	rt, g, done := r10Session(t, one, two, three)
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	started := make(chan string, 1)
	go func() {
		out, _ := rt.relayTool("relay_start", argStr(map[string]any{"profile": "claude"}), argNum(nil), argList(nil), false)
		started <- out
	}()
	g.waitEntered(t)
	evs := r10Quit(t, rt, g, 2, done)
	recv(t, started, "relay_start")
	st := state.Replay(evs)
	if rel := st.Relays["codex"]; rel == nil || !rel.Active() || rel.Model != "gpt-6.2-astra" {
		t.Errorf("codex on the record: %+v", rel)
	}
	if rel := st.Relays["claude"]; rel == nil || !rel.Active() {
		t.Errorf("claude on the record: %+v", rel)
	}
}

// A review's suspicion: a model change restarts Codex; a newer change is
// asked while it starts (it waits for that start); the session ends before
// the start is recorded. The newer change is not made, as nothing starts
// once the session is ending: the record says so as what it is, with what
// Codex runs, not as a start that was tried and could not be made.
func TestK5ANewerChangeTheEndOvertakesIsSaidTruthfully(t *testing.T) {
	one, two := newFakeSeat("one"), newFakeSeat("two")
	rt, g, done := r10Session(t, one, two)
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	rt.sync(func() {
		msg, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", "max"})
		t.Logf("newer change: %q %v", msg, err)
	})
	evs := r10Quit(t, rt, g, 1, done)
	said := false
	for _, ev := range evs {
		var d event.ErrorData
		if ev.Type != event.Error || ev.Decode(&d) != nil {
			continue
		}
		if strings.Contains(d.Text, "could not start") {
			t.Errorf("the record says a start was tried and failed: %q", d.Text)
		}
		said = said || d.Text == "Codex runs gpt-6.2-astra; the change to gpt-6.2-astra at max was not made, as the session ended first"
	}
	if !said {
		t.Error("the record does not say the newer change was not made, and what Codex runs")
	}
}

// A review's suspicion on df2c602: eagent types one of the agent's own
// commands (/clear) into an idle Codex and, while it is being typed, the
// model is changed. A command holds nothing back since df2c602, so the
// change stopped Codex under the typing. A change never cuts a send being
// typed: it waits for the command to be typed, then applies.
func TestK5AChangeDoesNotCutACommandBeingTyped(t *testing.T) {
	seat := newGateSeat("…")
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() { rt.st.Relays["codex"].State = "idle" })
	sent := make(chan string, 1)
	go func() {
		out, _ := rt.relayTool("relay_send", argStr(map[string]any{"text": "/clear"}), argNum(nil), argList(nil), false)
		sent <- out
	}()
	recv(t, seat.entered, "the command being typed")
	msg := k1Repick(t, rt)
	if k1Restarting(rt) {
		t.Errorf("the change started Codex again while /clear was being typed (%q)", msg)
	}
	close(seat.gate)
	if out := recv(t, sent, "relay_send"); strings.Contains(out, "not running") {
		t.Errorf("the command was cut: %q", out)
	}
	waitFor(t, "the change applied once the command is typed", func() bool { return k1Restarting(rt) })
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
}

// A start that comes up after the session's end stopped waiting for starts
// (relayOpsGrace) cannot be recorded: nothing is written after
// session.end, and its agent is stopped, as nothing would know it runs.
func TestK5AStartThatComesUpAfterTheEndIsNotRecorded(t *testing.T) {
	saved := relayOpsGrace
	relayOpsGrace = 10 * time.Millisecond
	t.Cleanup(func() { relayOpsGrace = saved })
	one, two := newFakeSeat("one"), newFakeSeat("two")
	rt, g, done := r10Session(t, one, two)
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	evs := r10Quit(t, rt, g, 0, done)
	g.release <- struct{}{}
	waitFor(t, "the late agent stopped", two.wasKilled)
	after, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(evs) || after[len(after)-1].Type != event.SessionEnd {
		t.Errorf("the record gained %d events after session.end", len(after)-len(evs))
	}
}
