package harness

import (
	"strings"
	"testing"
)

// Finding 9 of the ninth review: while a switch from Codex to Claude is
// under way, a model change for Codex is refused with what is happening,
// instead of "Codex is starting again; it changes to ... once it is up"
// (it never starts again), and nothing is left waiting for it.
func TestR9AModelChangeForAnAgentBeingReplacedIsRefused(t *testing.T) {
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	sw := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	var msg string
	var err error
	rt.sync(func() { msg, err = rt.repickRelay("codex", relayPick{"gpt-6.2-astra", "high"}) })
	if err == nil || !strings.Contains(err.Error(), "Claude") {
		t.Errorf("repick during the switch: %q, %v; want it refused, naming Claude", msg, err)
	}
	g.release <- struct{}{}
	recv(t, sw, "switch")
	rt.sync(func() {
		if _, _, waiting := rt.nextPick("codex"); waiting {
			t.Errorf("a change is left waiting for codex after it was replaced")
		}
	})
}
