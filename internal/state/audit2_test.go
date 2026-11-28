package state

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// State-level tests for the fixes made after the second behaviour audit.

// Upgraded is true once a subsession opened for the takeover of a log of the
// three-actor design exists with its dossier, and not before.
func TestAudit2UpgradedFollowsTheUpgradeSubsession(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.Assistant, event.LegacyOrchestrator, event.AssistantData{Text: "drafting", Usage: event.Usage{Input: 900, Output: 20}}))
	st := Replay(b.events)
	if !st.Legacy() || st.Upgraded() {
		t.Fatalf("before the takeover: legacy=%v upgraded=%v", st.Legacy(), st.Upgraded())
	}
	b.add(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "resumed by an eagent with one loop", NextFile: "2.jsonl"}))
	b.add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "2.jsonl", Index: 1, Reason: "upgrade"}))
	if st := Replay(b.events); st.Upgraded() {
		t.Fatal("upgraded before the upgrade's dossier was written")
	}
	b.add(event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "briefing", CoversSeq: 1}))
	if st := Replay(b.events); !st.Upgraded() {
		t.Fatal("not upgraded after the upgrade subsession")
	}
}

// A dossier's calls are all counted toward eagent's calls: Calls 3 adds 3,
// and Calls 0 still adds the one call it took.
func TestAudit2DossierCallsAreCounted(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "briefing", Calls: 3, ElapsedMS: 900, Usage: &event.Usage{Input: 3000, Output: 300}}))
	st := Replay(b.events)
	if got := st.Calls[event.ActorEagent]; got != 3 {
		t.Fatalf("after a dossier of 3 calls eagent has %d calls", got)
	}
	b.add(event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "briefing", Calls: 0, Usage: &event.Usage{Input: 1000, Output: 100}}))
	st = Replay(b.events)
	if got := st.Calls[event.ActorEagent]; got != 4 {
		t.Fatalf("after a dossier with no call count eagent has %d calls, want 4", got)
	}
}

// What an agent's screen showed when it got stuck, exited, or a message to
// it failed reaches the model's view up to 2000 characters.
func TestAudit2RelayDetailsReachTheViewUpTo2000Characters(t *testing.T) {
	detail := func(tag string, n int) string {
		end := "<" + tag + "-END>"
		return strings.Repeat("d", n-len(end)) + end
	}
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "blocked", Detail: detail("BLOCKED", 1990)}))
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: "r1", OK: false, Detail: detail("UNDELIVERED", 1990)}))
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "exited", Detail: detail("EXITED", 1990)}))
	all := texts(Replay(b.events).View(nil))
	for _, tag := range []string{"BLOCKED", "UNDELIVERED", "EXITED"} {
		if !strings.Contains(all, "<"+tag+"-END>") {
			t.Errorf("the %s detail of 1990 characters is cut in the view", tag)
		}
	}
	b = newBuilder()
	b.startClaude()
	// Past 2000 the middle goes: the start and the end of the screen stay.
	long := strings.Repeat("a", 1500) + "<MIDDLE>" + strings.Repeat("b", 1092)
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "blocked", Detail: long}))
	all = texts(Replay(b.events).View(nil))
	if strings.Contains(all, "<MIDDLE>") || !strings.Contains(all, strings.Repeat("a", 1300)) || !strings.Contains(all, strings.Repeat("b", 600)) {
		t.Error("a 2600-character detail is not cut to about 2000")
	}
}
