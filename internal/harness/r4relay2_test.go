package harness

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A switch from Codex to Claude whose brief never reached Claude (it is on
// its startup screens). Meanwhile a new, unrelated agent is started; the
// name "codex" is free again (the old Codex ended), so it is called
// "codex", and the user tells it something. Then Claude is switched to pi.
// pi's brief hands over from the first Codex (right) and quotes the user's
// sends to every relay named in the chain, by name, as they are on the
// record when pi is up: the instruction meant for the unrelated new Codex
// is quoted to pi as the user's instruction for this work.
func TestAChainedBriefLeavesOutWhatWasSaidToALaterAgentOfTheSameName(t *testing.T) {
	const unrelated = "only touch the docs directory in the other repo"
	first := &lingerSeat{newFakeSeat("one")}
	stuck := newClosingSeat("stuck")
	other := newFakeSeat("ok")
	final := newFakeSeat("done")
	g := useGatedLaunches(t, []relaySeat{first, stuck, other, final}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, r4Task)
	waitFor(t, "the task typed into Codex", func() bool { return len(first.sentTexts()) == 1 })
	r4CodexTurned(t, rt)

	sw1 := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"})
	g.waitEntered(t)
	g.release <- struct{}{}
	stuck.waitEntered(t, "You are taking over") // Claude's brief waits on its startup screens

	// An unrelated agent, started fresh; it gets the free name "codex".
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
	var name string
	rt.sync(func() {
		for n, rel := range rt.st.Relays {
			if rel.Active() && rel.Profile == "codex" {
				name = n
			}
		}
	})
	if name != "codex" {
		t.Skipf("the new agent is named %q, not a reuse of codex", name)
	}
	// The user tells the new Codex something (its own thread).
	rt.sync(func() {
		d := event.UserMessageData{Text: unrelated}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		rt.forwardUserMessage(rt.st.Relays["codex"], ev.Seq, d)
	})
	waitFor(t, "the unrelated instruction typed", func() bool { return len(other.sentTexts()) == 1 })

	sw2 := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "pi", "relay": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, sw1, "first switch")
	recv(t, sw2, "second switch")
	waitFor(t, "pi's brief typed", func() bool { return len(final.sentTexts()) >= 1 })
	brief := final.sentTexts()[0]
	if !strings.Contains(brief, r4Task) {
		t.Errorf("the task is missing from pi's brief:\n%s", brief)
	}
	if strings.Contains(brief, unrelated) {
		t.Errorf("pi's brief quotes a message the user sent to a different, later agent that reused the name codex:\n%s", brief)
	}
}
