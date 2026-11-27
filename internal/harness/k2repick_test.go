package harness

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// A model change asked while the seat is blocked for a reason that is not
// a dialog (a start that never became ready, an agent quiet for 10 minutes
// in a turn whose screen stays busy). At f36e19c the change started the
// agent again at once with the new model. Here it waits for an idle the
// seat never says.
func TestRV15ModelChangeOnAStuckSeatIsApplied(t *testing.T) {
	for _, detail := range []string{
		"still not ready after 25s; the screen ends:\n…",
		"no transcript activity for 10m; the screen ends:\n…",
	} {
		seat := newK1HeldSeat()
		g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
		rt := newLaunchRuntime(t)
		if strings.HasPrefix(detail, "no transcript") {
			seat.set("working")
			seat.emit(relay.Event{Kind: relay.KindState, State: "working"})
			k1Relay(t, rt, "the agent in a turn", func(rel *state.Relay) bool { return rel.State == "working" })
		}
		seat.set("blocked")
		seat.emit(relay.Event{Kind: relay.KindState, State: "blocked", Detail: detail})
		k1Relay(t, rt, "blocked recorded", func(rel *state.Relay) bool { return rel.State == "blocked" })
		msg := k1Repick(t, rt)
		if !k1Restarting(rt) {
			t.Fatalf("%q: the change was not applied to the stuck agent: %q", detail, msg)
		}
		g.waitEntered(t)
		g.release <- struct{}{}
		waitRelayStarts(t, rt)
		k1Relay(t, rt, "the new model", func(rel *state.Relay) bool { return rel.Model == "gpt-6.2-astra" })
	}
}
