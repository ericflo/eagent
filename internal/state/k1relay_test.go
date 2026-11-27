package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// k1state adds a relay.state for the Claude relay.
func k1state(b *builder, state, detail string) {
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: state, Detail: detail}))
}

func k1progress(b *builder, text string) {
	b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "claude", Text: text}))
}

// A11. pi writes progress while it waits on a dialog its extension
// reports, and its seat holds it blocked without saying a state again: the
// record stays blocked, with the progress kept, until the seat says it is
// not (relay.state).
func TestK1AProgressUnderADialogLeavesTheRelayBlocked(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	k1state(b, "working", "")
	k1state(b, "blocked", "Pi is waiting on a dialog: Allow rm -rf build?; see relay_screen and answer it with relay_control keys")
	k1progress(b, "Checking the build.")
	if rel := Replay(b.events).Relays["claude"]; rel.State != "blocked" || len(rel.Recent) != 1 {
		t.Fatalf("after progress under the dialog: %s, recent %q; want blocked, the progress kept", rel.State, rel.Recent)
	}
	k1state(b, "working", "")
	if st := Replay(b.events).Relays["claude"].State; st != "working" {
		t.Errorf("after the dialog: %s, want working", st)
	}
}

// A11, its bound. An agent blocked for going quiet (or for a slow start)
// that writes again is said working by its seat before its progress, and
// its turn comes before its idle (the harness drops that idle as already
// on the record): the record is working, and idle at the turn, as before.
func TestK1AQuietAgentGoesWorkingAndIdleAsBefore(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	k1state(b, "working", "")
	k1state(b, "blocked", "no transcript activity for 10m; the screen ends:\n…")
	k1state(b, "working", "")
	k1progress(b, "Still building.")
	if st := Replay(b.events).Relays["claude"].State; st != "working" {
		t.Fatalf("quiet agent writing again: %s, want working", st)
	}
	k1state(b, "blocked", "no transcript activity for 10m; the screen ends:\n…")
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "Built it."}))
	if rel := Replay(b.events).Relays["claude"]; rel.State != "idle" || rel.Turns != 1 {
		t.Errorf("quiet agent's turn: %s, %d turns; want idle, 1", rel.State, rel.Turns)
	}
}

// A11, its turn half: a turn that ends under the dialog leaves the record
// idle while the seat stays blocked.
func TestK1ATurnUnderADialogLeavesTheRelayBlocked(t *testing.T) {
	t.Skip("known, not fixed: a relay.turn does not say whether a dialog still holds the agent, and a quiet agent's turn, which comes before its idle, must make it idle (records already written have no idle after it)")
	b := newBuilder()
	b.startClaude()
	k1state(b, "working", "")
	k1state(b, "blocked", "Pi is waiting on a dialog: Allow rm -rf build?; see relay_screen and answer it with relay_control keys")
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "Done."}))
	if rel := Replay(b.events).Relays["claude"]; rel.State != "blocked" || rel.Turns != 1 {
		t.Errorf("after a turn under the dialog: %s, %d turns; want blocked, the turn counted", rel.State, rel.Turns)
	}
}
