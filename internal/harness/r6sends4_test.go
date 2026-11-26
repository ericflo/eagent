package harness

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

func timesIn(texts []string, s string) int {
	n := 0
	for _, t := range texts {
		n += strings.Count(t, s)
	}
	return n
}

// After a switch to Claude and back, the fresh Codex's handoff brief quotes
// what the user told the first Codex. relay_forward of that message is
// refused: forwarding it would type it in again as a new instruction.
func TestAnAgentBriefedWithAMessageIsNotForwardedItAgain(t *testing.T) {
	codex1, claude, codex2 := newFakeSeat("one"), newFakeSeat("two"), newFakeSeat("three")
	g := useGatedLaunches(t, []relaySeat{codex1, claude, codex2}, nil)
	rt := newLaunchRuntime(t)
	seq := userSaysWith(t, rt, "check the version")
	first := lastSendID(rt)
	waitFor(t, "the first send's arrival", func() bool {
		ok := false
		rt.sync(func() { ok = rt.st.SendOutcome(first) == state.SendArrived })
		return ok
	})
	for _, p := range []string{"claude", "codex"} {
		out := relayToolAsync(rt, map[string]any{"action": "switch", "profile": p})
		g.waitEntered(t)
		g.release <- struct{}{}
		recv(t, out, "switch to "+p)
	}
	if n := timesIn(codex2.sentTexts(), "check the version"); n != 1 {
		t.Fatalf("codex after the switch back was told the message %d times (want once, in its brief): %q", n, codex2.sentTexts())
	}
	text, isErr := forward(rt, map[string]any{"seq": seq})
	if n := timesIn(codex2.sentTexts(), "check the version"); !isErr || n != 1 {
		t.Errorf("relay_forward of a message the running agent's brief quotes: %q (error %v); codex has it %d times", text, isErr, n)
	}
}

// A relay_forward made while a fresh start under a stopped name launches is
// held and typed into the new agent once it is up; a second forward of the
// same message to that agent is refused.
func TestASendHeldForAStartCountsForTheAgentItReached(t *testing.T) {
	codex1, codex2 := newFakeSeat("one"), newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{codex1, codex2}, nil)
	rt := newLaunchRuntime(t)
	if text, isErr := rt.relayTool("relay_control", argStr(map[string]any{"action": "stop"}), argNum(nil), argList(nil), false); isErr {
		t.Fatalf("stop: %q", text)
	}
	var seq int64
	rt.sync(func() {
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "check the version", Held: "no agent is running"}))
		seq = ev.Seq
	})
	out := make(chan string, 1)
	go func() {
		text, _ := rt.relayTool("relay_start", argStr(map[string]any{"profile": "codex"}), argNum(nil), argList(nil), false)
		out <- text
	}()
	g.waitEntered(t)
	if text, isErr := forward(rt, map[string]any{"seq": seq, "relay": "codex"}); isErr || !strings.Contains(text, "is starting") {
		t.Fatalf("forward while it starts: %q (error %v)", text, isErr)
	}
	g.release <- struct{}{}
	recv(t, out, "relay_start")
	waitFor(t, "the held send typed into the new agent", func() bool { return timesIn(codex2.sentTexts(), "check the version") == 1 })
	text, isErr := forward(rt, map[string]any{"seq": seq, "relay": "codex"})
	if n := timesIn(codex2.sentTexts(), "check the version"); !isErr || n != 1 {
		t.Errorf("relay_forward again to the agent that has the message: %q (error %v); it has it %d times", text, isErr, n)
	}
}
