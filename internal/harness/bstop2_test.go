package harness

import (
	"slices"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A restart is under way and a user message waits for it (held, never
// typed). The relay is stopped while the restart starts. The held message
// was never typed: it should end once, not_sent, not first "unconfirmed".
func TestASendHeldForARestartThatIsStoppedIsReportedOnceAsNotSent(t *testing.T) {
	old, next := newFakeSeat("one"), newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	userSays(t, rt, "also sweep the floor")
	var heldID string
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelaySendData
			if ev.Type == event.RelaySend && ev.Decode(&d) == nil && d.Text == "also sweep the floor" {
				heldID = d.ID
			}
		}
	})
	if heldID == "" {
		t.Fatalf("the held message was not recorded as a send")
	}
	args := map[string]any{"action": "stop"}
	if text, isErr := rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false); isErr {
		t.Fatalf("stop: %s", text)
	}
	g.release <- struct{}{}
	waitFor(t, "the held message settled not_sent", func() bool {
		var done bool
		rt.sync(func() {
			for _, ev := range rt.st.Events {
				var d event.RelayDeliveredData
				if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == heldID && d.Outcome == event.DeliveryNotSent {
					done = true
				}
			}
		})
		return done
	})
	var evs []event.Event
	rt.sync(func() { evs = slices.Clone(rt.st.Events) })
	var got []string
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == heldID {
			got = append(got, d.Outcome+": "+d.Detail)
		}
	}
	if len(got) != 1 {
		t.Fatalf("held message (never typed) has %d outcomes: %q", len(got), got)
	}
	if s := next.sentTexts(); len(s) != 0 {
		t.Fatalf("typed into the new agent: %q", s)
	}
}
