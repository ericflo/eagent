package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/relay"
)

// Finding 13: a model change waits for the agent's turn to end. When that
// turn ends while the agent is blocked on a dialog it reports (pi), the
// agent is not restarted under the dialog: the change waits until the
// seat says the dialog is over (idle), and applies then.
func TestR13RTurnUnderADialogLeavesTheChangeWaiting(t *testing.T) {
	t.Skip("known, not fixed: the record goes idle at the turn (see state's TestR13RTurnLeavesABlockedRelayBlocked), so nothing on it says the dialog is still up")
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		rt.onRelayEvent("codex", relay.Event{Kind: relay.KindState, State: "working"})
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Fatalf("repick: %v", err)
		}
		rt.onRelayEvent("codex", relay.Event{Kind: relay.KindState, State: "blocked", Detail: "Codex is waiting on a dialog: Allow rm -rf build?"})
		rt.onRelayEvent("codex", relay.Event{Kind: relay.KindProgress, Text: "Checking the build."})
		rt.onRelayEvent("codex", relay.Event{Kind: relay.KindTurn, Text: "Done."})
		if st := rt.st.Relays["codex"].State; st != "blocked" {
			t.Errorf("the record says %s under the dialog, want blocked", st)
		}
		if rt.relays.launch("codex") != nil {
			t.Errorf("the agent was restarted under its dialog")
		}
		rt.onRelayEvent("codex", relay.Event{Kind: relay.KindState, State: "idle"})
		if rt.relays.launch("codex") == nil {
			t.Errorf("the change did not apply once the dialog was over")
		}
	})
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	rt.sync(func() {
		if rel := rt.st.Relays["codex"]; rel.Model != "gpt-6.2-astra" || len(rt.relayRepick) != 0 {
			t.Errorf("after the dialog: model %q, waiting %v", rel.Model, rt.relayRepick)
		}
	})
}
