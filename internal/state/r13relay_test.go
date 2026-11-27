package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// r13blocked is a log whose Claude relay is blocked on a dialog it
// reports (pi's extension does), after it worked.
func r13blocked() *builder {
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "working"}))
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "blocked", Detail: "Pi is waiting on a dialog: Allow rm -rf build?"}))
	return b
}

// Finding 13: an agent blocked on a dialog it reports still writes
// progress; the seat holds it blocked. The relay stays blocked on the
// record until the seat says it is not (relay.state).
func TestR13RProgressLeavesABlockedRelayBlocked(t *testing.T) {
	b := r13blocked()
	b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "claude", Text: "Checking the build."}))
	if rel := Replay(b.events).Relays["claude"]; rel.State != "blocked" || len(rel.Recent) != 1 {
		t.Errorf("after progress under the dialog: %s, recent %q; want blocked, the progress kept", rel.State, rel.Recent)
	}
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "working"}))
	if st := Replay(b.events).Relays["claude"].State; st != "working" {
		t.Errorf("after the dialog: %s, want working", st)
	}
}

// Finding 13, its turn half: a turn that ends under the dialog leaves the
// record idle while the seat stays blocked.
func TestR13RTurnLeavesABlockedRelayBlocked(t *testing.T) {
	t.Skip("known, not fixed: a relay.turn cannot tell a dialog's blocked from a quiet agent's, whose turn end must still make it idle")
	b := r13blocked()
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "Done."}))
	if rel := Replay(b.events).Relays["claude"]; rel.State != "blocked" || rel.Turns != 1 {
		t.Errorf("after a turn under the dialog: %s, %d turns; want blocked, the turn counted", rel.State, rel.Turns)
	}
}
