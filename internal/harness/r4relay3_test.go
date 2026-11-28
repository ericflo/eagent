package harness

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

const (
	r43CodexLog  = "/t/codex-session.jsonl"
	r43ClaudeLog = "/t/claude-session.jsonl"
	r43PiLog     = "/t/pi-session.jsonl"
)

// r43Hang is an agent that takes what it is typed and is still in its first
// turn: it never finishes one.
func r43Hang() *fakeSeat {
	s := newFakeSeat("never")
	s.hang = true
	return s
}

// r43Inner records that a relay found its own session log.
func r43Inner(t *testing.T, rt *Runtime, name, log string) {
	t.Helper()
	rt.sync(func() {
		rt.append(event.New(event.RelayInner, event.ActorHarness, event.RelayInnerData{Name: name, InnerID: name + "-conv", Transcript: log}))
	})
}

// r43Step runs a relay_control action that launches an agent and waits for
// it to be up.
func r43Step(t *testing.T, rt *Runtime, g *gatedLaunches, args map[string]any) string {
	t.Helper()
	out := relayToolAsync(rt, args)
	g.waitEntered(t)
	g.release <- struct{}{}
	text := recv(t, out, fmt.Sprint(args["action"], " ", args["profile"]))
	waitRelayStarts(t, rt)
	return text
}

// r43Start has Codex given the task, its turn and session log on the
// record.
func r43Start(t *testing.T, rest ...relaySeat) (*Runtime, *gatedLaunches, string) {
	t.Helper()
	codex := &lingerSeat{newFakeSeat("one")}
	g := useGatedLaunches(t, append([]relaySeat{codex}, rest...), nil)
	rt := newLaunchRuntime(t)
	r43Inner(t, rt, "codex", r43CodexLog)
	userSays(t, rt, r4Task)
	waitFor(t, "the task typed into Codex", func() bool { return len(codex.sentTexts()) == 1 })
	r4CodexTurned(t, rt)
	var dir string
	rt.sync(func() { dir = rt.st.Relays["codex"].Dir })
	return rt, g, dir
}

func r43Brief(dir, between string, said ...string) string {
	b := fmt.Sprintf("You are taking over this work from Codex, which was working in %s. Its full session log is %s (JSONL) if you need detail.", dir, r43CodexLog)
	if between != "" {
		b += " " + between
	}
	b += "\n\nThe user's instructions so far, verbatim, oldest first:\n"
	for _, s := range said {
		b += "- " + s + "\n"
	}
	return b + "\nCodex's last report:\none\n\nCheck the working tree and recent commits, then continue where it left off."
}

const r43ClaudeTold = "Claude (its session log is " + r43ClaudeLog + ") was started to take over in between and stopped before it finished a turn; it may have changed files."

func r43Typed(t *testing.T, seat *fakeSeat, want ...string) {
	t.Helper()
	waitFor(t, "the last agent typed into", func() bool { return len(seat.sentTexts()) >= len(want) })
	got := seat.sentTexts()
	if len(got) != len(want) {
		t.Fatalf("typed %d texts, want %d: %q", len(got), len(want), firstWords(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("text %d typed:\n%s\nwant:\n%s", i, got[i], want[i])
		}
	}
}

// Codex has the task; a switch to Claude, whose brief is typed; a switch to
// pi before Claude finished a turn. pi hands over from Codex (its report and
// session log), quotes the task, and names Claude as told the work but
// stopped before it finished a turn.
func TestSwitchFromABriefedAgentThatHasNotFinishedATurnHandsOverFromTheOneBefore(t *testing.T) {
	claude, pi := r43Hang(), newFakeSeat("done")
	rt, g, dir := r43Start(t, claude, pi)
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"})
	waitFor(t, "Claude's brief typed", func() bool { return len(claude.sentTexts()) == 1 })
	r43Inner(t, rt, "claude", r43ClaudeLog)
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "pi", "relay": "claude"})
	r43Typed(t, pi, r43Brief(dir, r43ClaudeTold, r4Task))
}

// The same with a message the user typed into Claude before the second
// switch: it is quoted once, after the task, and not typed again.
func TestSwitchFromABriefedAgentQuotesWhatTheUserToldItOnce(t *testing.T) {
	claude, pi := r43Hang(), newFakeSeat("done")
	rt, g, dir := r43Start(t, claude, pi)
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"})
	waitFor(t, "Claude's brief typed", func() bool { return len(claude.sentTexts()) == 1 })
	r43Inner(t, rt, "claude", r43ClaudeLog)
	userSays(t, rt, r4Correction)
	waitFor(t, "the correction typed into Claude", func() bool { return len(claude.sentTexts()) == 2 })
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "pi", "relay": "claude"})
	r43Typed(t, pi, r43Brief(dir, r43ClaudeTold, r4Task, r4Correction))
}

// Once Claude has finished a turn after its brief, its own report describes
// the work: the switch to pi types a direct brief from Claude. It still
// quotes the task the user gave Codex, whose work Claude took over.
func TestSwitchFromABriefedAgentThatFinishedATurnIsADirectHandOver(t *testing.T) {
	claude, pi := newFakeSeat("two"), newFakeSeat("done")
	rt, g, _ := r43Start(t, claude, pi)
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"})
	waitFor(t, "Claude's turn on the record", func() bool {
		n := 0
		rt.sync(func() { n = rt.st.Relays["claude"].Turns })
		return n == 1
	})
	r43Inner(t, rt, "claude", r43ClaudeLog)
	var dir string
	rt.sync(func() { dir = rt.st.Relays["claude"].Dir })
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "pi", "relay": "claude"})
	want := fmt.Sprintf("You are taking over this work from Claude, which was working in %s. Its full session log is %s (JSONL) if you need detail.\n\nThe user's instructions so far, verbatim, oldest first:\n- %s\n\nClaude's last report:\ntwo\n\nCheck the working tree and recent commits, then continue where it left off.", dir, r43ClaudeLog, r4Task)
	r43Typed(t, pi, want)
}

// Codex, then Claude (its brief never typed: it stays on its startup
// screens), then pi (its brief typed, no turn finished), then Antigravity.
// Both kinds of agent in between are named, each truly, in order.
func TestChainWithAnUnbriefedAndABriefedAgentNamesEachTruly(t *testing.T) {
	claude, pi, ag := newClosingSeat("stuck"), r43Hang(), newFakeSeat("done")
	rt, g, dir := r43Start(t, claude, pi, ag)
	sw1 := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"})
	g.waitEntered(t)
	g.release <- struct{}{}
	claude.waitEntered(t, "You are taking over")
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "pi", "relay": "claude"})
	recv(t, sw1, "first switch")
	waitFor(t, "pi's brief typed", func() bool { return len(pi.sentTexts()) == 1 })
	r43Inner(t, rt, "pi", r43PiLog)
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "antigravity", "relay": "pi"})
	between := "Claude, then Pi (its session log is " + r43PiLog + ") was started to take over in between and stopped before it finished a turn; it may have changed files."
	r43Typed(t, ag, r43Brief(dir, between, r4Task))
}

// Claude, briefed and still in its first turn, is restarted (same
// conversation) and then switched to pi: the chain holds.
func TestRestartOfABriefedAgentKeepsTheChain(t *testing.T) {
	claude, again, pi := r43Hang(), r43Hang(), newFakeSeat("done")
	rt, g, dir := r43Start(t, claude, again, pi)
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"})
	waitFor(t, "Claude's brief typed", func() bool { return len(claude.sentTexts()) == 1 })
	r43Inner(t, rt, "claude", r43ClaudeLog)
	r43Step(t, rt, g, map[string]any{"action": "restart", "relay": "claude"})
	if got := again.sentTexts(); len(got) != 0 {
		t.Fatalf("the restart typed %q", firstWords(got))
	}
	// The restarted agent finds its session log again, as a real one does.
	r43Inner(t, rt, "claude", r43ClaudeLog)
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "pi", "relay": "claude"})
	r43Typed(t, pi, r43Brief(dir, r43ClaudeTold, r4Task))
}

// A later, unrelated agent started under the name "codex" while Claude is
// briefed and still working: what the user tells it is not quoted to pi.
func TestBriefedChainLeavesOutALaterAgentOfTheSameName(t *testing.T) {
	const unrelated = "only touch the docs directory in the other repo"
	claude, other, pi := r43Hang(), newFakeSeat("ok"), newFakeSeat("done")
	rt, g, dir := r43Start(t, claude, other, pi)
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"})
	waitFor(t, "Claude's brief typed", func() bool { return len(claude.sentTexts()) == 1 })
	r43Inner(t, rt, "claude", r43ClaudeLog)
	startOut := make(chan string, 1)
	go func() {
		args := map[string]any{"profile": "codex"}
		text, _ := rt.relayTool("relay_start", argStr(args), argNum(args), argList(args), false)
		startOut <- text
	}()
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, startOut, "relay_start")
	waitRelayStarts(t, rt)
	rt.sync(func() {
		if rel := rt.st.Relays["codex"]; rel == nil || !rel.Active() {
			t.Fatal("the new agent did not take the name codex")
		}
		d := event.UserMessageData{Text: unrelated}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		rt.forwardUserMessage(rt.st.Relays["codex"], ev.Seq, d)
	})
	waitFor(t, "the unrelated instruction typed", func() bool { return len(other.sentTexts()) == 1 })
	r43Step(t, rt, g, map[string]any{"action": "switch", "profile": "pi", "relay": "claude"})
	r43Typed(t, pi, r43Brief(dir, r43ClaudeTold, r4Task))
	if strings.Contains(pi.sentTexts()[0], unrelated) {
		t.Error("the unrelated instruction was quoted")
	}
}
