package relay

import (
	"testing"
)

// SR7-3, a reattach's half: a block a reattach kept or made (hung, not yet
// judged) that the first screen check finds stuck is said, as one that
// turns stuck later is.
func TestK9AReattachedBlockFoundStuckIsSaid(t *testing.T) {
	h := &fakeHost{alive: true, screen: "$ "}
	s := newSeat(Spec{}, codexAgent{}, h, "k9-reattach-stuck", "thread-1")
	s.state, s.hung = "blocked", true // as attachSeat leaves a quiet turn
	s.checkScreen(s.stateAt)
	if !s.Snapshot().Stuck {
		t.Fatal("not stuck at a shell prompt")
	}
	evs := drain(s.events)
	if len(evs) != 1 || evs[0].Kind != KindState || evs[0].State != "blocked" || !evs[0].Stuck {
		t.Fatalf("events %+v; want blocked said again, stuck", evs)
	}
}
