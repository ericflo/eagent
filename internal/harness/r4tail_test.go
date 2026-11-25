package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

var r4t0 = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

func r4at(sec int) time.Time { return r4t0.Add(time.Duration(sec) * time.Second) }

// reuseRecord is a log where a custom relay "review" was Codex, finished a
// turn and was stopped, and the name was then taken by pi.
func reuseRecord() []event.Event {
	var evs []event.Event
	add := func(sec int, ev event.Event) {
		ev.Seq, ev.Time = int64(len(evs)+1), r4at(sec)
		ev.Source = event.Source{File: "1.jsonl", Line: len(evs) + 1}
		evs = append(evs, ev)
	}
	add(0, event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "s1", Cwd: "/p"}))
	add(0, event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1.jsonl", Reason: "new"}))
	add(1, event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "review", Profile: "review", Agent: "codex", AgentLabel: "Codex", Dir: "/p", Host: "tmux", Target: "t"}))
	add(2, event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "review", Text: "The unit tests passed."}))
	add(3, event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "review", Reason: "the user stopped it"}))
	add(4, event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "review", Profile: "review", Agent: "pi", AgentLabel: "Pi", Dir: "/q", Host: "tmux", Target: "t"}))
	return evs
}

// What the harness writes about an older turn end (the dossier's index and
// facts, the tail's "who finished") names the agent that ended it, after
// its name has been taken by another.
func TestHarnessTextAboutAnOlderTurnNamesItsAgent(t *testing.T) {
	r := &Runtime{st: state.Replay(reuseRecord())}
	if got := r.turnEndWho("the agent"); got != "Codex" {
		t.Fatalf("turnEndWho: %q", got)
	}
	if idx := r.dossierIndex(); !strings.Contains(idx, "Codex finished a turn: The unit tests passed.") || strings.Contains(idx, "Pi finished") {
		t.Fatalf("dossier index:\n%s", idx)
	}
	if facts := r.dossierFacts(r.st.LastSeq(), ""); !strings.Contains(facts, "1.jsonl:4] Codex: The unit tests passed.") {
		t.Fatalf("dossier facts:\n%s", facts)
	}
}

// Steps the earlier holder of a name took are not listed as the new
// agent's in the tail.
func TestStepsOfAnEarlierHolderAreNotTheNewAgents(t *testing.T) {
	evs := reuseRecord()
	st := state.Replay(evs)
	rel := st.Relays["review"]
	s := &relaySeats{}
	s.noteStep("review", "Codex ran the tests", r4at(2))
	s.noteStep("review", "Pi read the diff", r4at(5))
	got := s.stepsSince("review", stepsFrom(rel, r4t0))
	if len(got) != 1 || got[0].text != "Pi read the diff" {
		t.Fatalf("steps listed for pi: %+v", got)
	}
}
