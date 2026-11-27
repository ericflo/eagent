package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// The round-16 reader's suspicion on 4f9fb94: a restart finishes with a
// newer change waiting, and the restart that change asks for cannot be
// begun (the session is ending). The change is dropped; that is said, as
// when a waiting change cannot start the agent again anywhere else
// (applyWaitingRepick), not left silent.
func TestK3AChainedRestartThatCannotBeginIsSaid(t *testing.T) {
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("zero"), newFakeSeat("one")}, nil)
	rt := newLaunchRuntime(t)
	k1Repick(t, rt)
	g.waitEntered(t)
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{}); err != nil {
			t.Errorf("second change: %v", err)
		}
		rt.ending = true // what beginShutdown sets: no agent is started now
	})
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	var said bool
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.ErrorData
			if ev.Type == event.Error && ev.Decode(&d) == nil && d.Where == "relay" {
				said = true
			}
		}
	})
	if !said {
		t.Error("the change that could not be applied was dropped without a word")
	}
}
