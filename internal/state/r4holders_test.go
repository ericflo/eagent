package state

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// The second agent under a reused name is restarted in place (a model
// change from settings, a relaunch: a RelayStart with Resumed set while it
// still runs). Its earlier lines must stay under its own name: the
// conversation already sent to the provider stays byte for byte.
func TestARestartOfANamesSecondAgentKeepsItsEarlierLines(t *testing.T) {
	b := reviewByCodex()
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "review", Profile: "review", Agent: "pi", AgentLabel: "Pi", Label: "pi", Dir: "/q", Host: "tmux", Target: "eagent-1-review", InnerID: "pi-1"}))
	piTurn := b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "review", Text: "Looked again."}))
	before := strings.TrimSuffix(flat(Replay(b.events).View(nil)), "\x01")
	if !strings.Contains(before, "<Pi> Looked again.") {
		t.Fatalf("pi's report is not under its name:\n%s", before)
	}
	// Restarted on its own conversation with another model.
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "review", Profile: "review", Agent: "pi", AgentLabel: "Pi", Label: "pi", Dir: "/q", Host: "tmux", Target: "eagent-1-review", InnerID: "pi-1", Resumed: true, Model: "other"}))
	st := Replay(b.events)
	if rel := st.RelayAt("review", piTurn.Seq); rel == nil || rel.Agent != "pi" {
		t.Errorf("RelayAt pi's turn after pi was restarted: %+v", rel)
	}
	after := flat(st.View(nil))
	if !strings.HasPrefix(after, before) {
		i := 0
		for i < len(before) && i < len(after) && before[i] == after[i] {
			i++
		}
		t.Fatalf("restarting pi changed what was rendered before it, at byte %d:\nbefore: %q\nafter:  %q", i, before[max(0, i-60):min(len(before), i+60)], after[max(0, i-60):min(len(after), i+60)])
	}
}

// The same after a stop and a resume of the second holder on its own
// conversation (/stop then a resume): the stop line and its turns stay
// under pi's name.
func TestAResumeOfANamesStoppedSecondAgentKeepsItsEarlierLines(t *testing.T) {
	b := reviewByCodex()
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "review", Profile: "review", Agent: "pi", AgentLabel: "Pi", Label: "pi", Dir: "/q", Host: "tmux", Target: "eagent-1-review", InnerID: "pi-1"}))
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "review", Text: "Looked again."}))
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "review", Reason: "the user stopped it"}))
	before := strings.TrimSuffix(flat(Replay(b.events).View(nil)), "\x01")
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "review", Profile: "review", Agent: "pi", AgentLabel: "Pi", Label: "pi", Dir: "/q", Host: "tmux", Target: "eagent-1-review", InnerID: "pi-1", Resumed: true}))
	after := flat(Replay(b.events).View(nil))
	if !strings.HasPrefix(after, before) {
		i := 0
		for i < len(before) && i < len(after) && before[i] == after[i] {
			i++
		}
		t.Fatalf("resuming pi changed what was rendered before it, at byte %d:\nbefore: %q\nafter:  %q", i, before[max(0, i-60):min(len(before), i+60)], after[max(0, i-60):min(len(after), i+60)])
	}
}
