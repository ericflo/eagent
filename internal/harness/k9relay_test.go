package harness

// The sixteenth review of the relay area, sixth pass (sr6).

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// SR6-1. Codex is in a long turn (a build, a test suite, a subagent: its
// transcript says nothing for ten minutes). The user's next message is
// typed into it while it works, and Codex holds it for its next step. The
// user then picks another model; the change is told it waits "when the
// turn ends". Ten minutes into the quiet the seat calls the agent blocked
// (no dialog), and since fbff884 the waiting change applies right then:
// the agent is killed mid-turn, and the message it held is reported
// unconfirmed (it may never reach the agent). On main a waiting change
// applied only at a turn's end or an idle state; blocked left it waiting.
func TestSR6AWaitingChangeKillsALongQuietTurn(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	seat.say(relay.Event{Kind: relay.KindState, State: "working"})
	k1Relay(t, rt, "the agent in a turn", func(rel *state.Relay) bool { return rel.State == "working" })
	_, id := k2Forward(t, rt, seat, "also update the changelog when you're done", "queue")
	msg := k1Repick(t, rt)
	if !strings.Contains(msg, "when the turn ends") {
		t.Fatalf("repick in a turn: %q", msg)
	}
	// Ten minutes of transcript silence inside the turn (checkScreen).
	seat.say(relay.Event{Kind: relay.KindState, State: "blocked", Detail: "no transcript activity for 10m; the screen ends:\n• Running go test ./... (12m 03s • esc to interrupt)"})
	// Decided in the step that records the block (k9: no 2 s wait).
	k1Relay(t, rt, "blocked recorded", func(rel *state.Relay) bool { return rel.State == "blocked" })
	restarted := k1Restarting(rt)
	if !restarted {
		return
	}
	t.Errorf("the change was told it waits until the turn ends (%q), but it restarted the agent while the turn was still running (blocked for transcript silence)", msg)
	seat.waitGone(t)
	seat.end()
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	waitFor(t, "the message's outcome", func() bool { return len(deliveredFor(rt, id)) > 0 })
	t.Errorf("the user's message Codex held for its next step: %q %+v", k2Outcome(rt, id), deliveredFor(rt, id))
}
