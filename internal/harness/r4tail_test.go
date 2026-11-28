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

// The same step taken just before and just after eagent last spoke is
// movement: the run of repeats goes on after that moment, and the list of
// what the agent did since then shows it, counted from then.
func TestARunOfIdenticalStepsAcrossTheLastMessageIsMovement(t *testing.T) {
	s := &relaySeats{}
	s.noteStep("codex", "Ran go test ./...", r4at(1))
	s.noteStep("codex", "Ran go test ./...", r4at(2))
	said := r4at(3)
	s.noteStep("codex", "Ran go test ./...", r4at(4))
	s.noteStep("codex", "Ran go test ./...", r4at(5))
	s.noteStep("codex", "Ran go test ./...", r4at(6))

	total, after := s.stepped(said)
	if total != 5 || !after {
		t.Fatalf("stepped: %d steps, after %v; want 5, true", total, after)
	}
	got := s.stepsSince("codex", said)
	if len(got) != 1 || got[0].text != "Ran go test ./..." || got[0].n != 3 || !got[0].at.Equal(r4at(4)) {
		t.Fatalf("stepsSince: %+v; want one step, three times since, first at +4s", got)
	}
	// The whole run is still there for a moment before it began.
	if all := s.stepsSince("codex", r4t0); len(all) != 1 || all[0].n != 5 || !all[0].at.Equal(r4at(1)) {
		t.Fatalf("stepsSince the start: %+v", all)
	}
}

// A run of identical steps that ended before eagent last spoke is not news.
func TestARunOfIdenticalStepsBeforeTheLastMessageIsNotNew(t *testing.T) {
	s := &relaySeats{}
	s.noteStep("codex", "Ran go test ./...", r4at(1))
	s.noteStep("codex", "Ran go test ./...", r4at(2))
	if _, after := s.stepped(r4at(3)); after {
		t.Fatal("a run that ended before the moment counted as movement")
	}
	if got := s.stepsSince("codex", r4at(3)); len(got) != 0 {
		t.Fatalf("stepsSince: %+v", got)
	}
}

// Steps without repeats, and runs wholly after the moment, are listed as
// they always were.
func TestStepsWithoutAStraddlingRunAreListedAsBefore(t *testing.T) {
	s := &relaySeats{}
	s.noteStep("codex", "Read main.go", r4at(1))
	s.noteStep("codex", "Edited main.go", r4at(4))
	s.noteStep("codex", "Ran go build", r4at(5))
	s.noteStep("codex", "Ran go build", r4at(6))
	got := s.stepsSince("codex", r4at(3))
	if len(got) != 2 || got[0].text != "Edited main.go" || got[0].n != 1 || !got[0].at.Equal(r4at(4)) || got[1].n != 2 || !got[1].at.Equal(r4at(5)) {
		t.Fatalf("stepsSince: %+v", got)
	}
}

// A long run keeps a bounded memory of its repeats and still counts the
// ones since a moment within the kept stretch.
func TestALongRunOfIdenticalStepsIsCountedFromTheMoment(t *testing.T) {
	s := &relaySeats{}
	n := relayRepeatsKept + 50
	for i := 0; i < n; i++ {
		s.noteStep("codex", "Polled the build", r4at(i))
	}
	got := s.stepsSince("codex", r4at(n-11))
	if len(got) != 1 || got[0].n != 10 || !got[0].at.Equal(r4at(n-10)) {
		t.Fatalf("stepsSince: %+v", got)
	}
	if all := s.stepsSince("codex", r4t0.Add(-time.Second)); len(all) != 1 || all[0].n != n {
		t.Fatalf("whole run: %+v", all)
	}
	if l := len(s.steps["codex"][0].times); l > relayRepeatsKept {
		t.Fatalf("%d repeat times kept", l)
	}
}

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
