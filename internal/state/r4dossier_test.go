package state

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// r4Record is a record numbered as the store would number it.
func r4Record(evs ...event.Event) []event.Event {
	for i := range evs {
		evs[i].Seq = int64(i + 1)
	}
	return evs
}

// A restart that stopped after the new file's subsession.start and before
// its dossier: the new file is not yet the conversation, so what the user
// said before it is still in what the model sees.
func TestUnfinishedRestartDoesNotEmptyTheConversation(t *testing.T) {
	evs := r4Record(
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "s"}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1000000000001.jsonl", Index: 0, Reason: "new"}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "use only the archived temperature readings"}),
		event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "context full", NextFile: "1000000000002.jsonl", InputTokens: 25000}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1000000000002.jsonl", Index: 1, Reason: "rollover"}),
		event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{}),
	)
	st := Replay(evs)
	if !st.Unfinished() {
		t.Fatal("a restart with no dossier is not seen as unfinished")
	}
	var all strings.Builder
	for _, m := range st.View(nil) {
		all.WriteString(m.Text)
	}
	if !strings.Contains(all.String(), "use only the archived temperature readings") {
		t.Fatalf("the conversation lost what the user said: %q", all.String())
	}
	// Once the dossier lands, the conversation is the new one.
	evs = append(evs, event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "context full", NextFile: "1000000000003.jsonl"}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1000000000003.jsonl", Index: 2, Reason: "rollover"}),
		event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "briefing", CoversSeq: 6}))
	st = Replay(r4Record(evs...))
	if st.Unfinished() || st.Conversation() != st.Current() {
		t.Fatal("a finished restart is still taken for an unfinished one")
	}
	if v := st.View(nil); len(v) != 1 || !strings.HasSuffix(v[0].Text, "briefing") {
		t.Fatalf("the finished conversation renders %d messages", len(v))
	}
}

// A session of the earlier design whose upgrade stopped before its dossier
// is not taken over yet: the next run writes that dossier again.
func TestUpgradeWithoutItsDossierIsNotAnUpgrade(t *testing.T) {
	evs := r4Record(
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "s", Models: map[string]string{"orchestrator": "glm-5"}}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1000000000001.jsonl", Index: 0, Reason: "new"}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "write the release notes"}),
		event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "resumed by an eagent with one loop", NextFile: "1000000000002.jsonl"}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1000000000002.jsonl", Index: 1, Reason: "upgrade"}),
	)
	st := Replay(evs)
	if !st.Legacy() || st.Upgraded() {
		t.Fatalf("legacy %v, upgraded %v: an upgrade with no dossier counts as done", st.Legacy(), st.Upgraded())
	}
	st = Replay(r4Record(append(evs, event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "briefing", CoversSeq: 3}))...))
	if !st.Upgraded() {
		t.Fatal("an upgrade with its dossier is not one")
	}
}

// A session of the earlier design that stopped while its own rollover's
// dossier was being written (that design wrote it later, from a task) is
// not taken for an unfinished restart of this one: it renders as it did.
func TestOldDesignsRolloverWithoutADossierRendersAsBefore(t *testing.T) {
	evs := r4Record(
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "s", Models: map[string]string{"orchestrator": "glm-5"}}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1000000000001.jsonl", Index: 0, Reason: "new"}),
		event.New(event.Assistant, event.LegacyOrchestrator, event.AssistantData{Text: "drafting"}),
		event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "context full", NextFile: "1000000000002.jsonl"}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1000000000002.jsonl", Index: 1, Reason: "rollover"}),
	)
	st := Replay(evs)
	if st.Unfinished() || st.Conversation() != st.Current() {
		t.Fatal("the earlier design's rollover is taken for an unfinished restart")
	}
}
