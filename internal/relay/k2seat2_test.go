package relay

import (
	"fmt"
	"testing"
	"time"
)

// claudeTurns feeds Claude transcript lines to a working seat and counts
// the turns it reports.
func claudeTurns(lines ...string) int {
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.state = "working"
	p := &claudeParser{}
	for _, l := range lines {
		for _, o := range p.line([]byte(l)) {
			s.fold(o)
		}
	}
	n := 0
	for _, ev := range drain(s.events) {
		if ev.Kind == KindTurn {
			n++
		}
	}
	return n
}

// rv16 reader (krseat2): Claude's API error answer ends the turn (28097f3).
// Should Claude still write a turn_duration for that query, the turn is
// reported once, not twice.
func TestRV16APIErrorThenTurnDurationReportsOneTurn(t *testing.T) {
	if n := claudeTurns(
		claudeUser(time.Now(), "run the build"),
		fmt.Sprintf(`{"type":"assistant","timestamp":"%s","isApiErrorMessage":true,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 529 Overloaded"}]}}`, stamp(time.Now())),
		k1TurnEnd(),
	); n != 1 {
		t.Fatalf("%d turns reported for one prompt; want 1", n)
	}
}

// What stays a turn: one whose prompt the parser never took for a prompt
// (a task notification Claude runs as a meta line) still ends at its
// turn_duration, and a turn after an API error ended the one before is
// reported at its own.
func TestK2TurnDurationStillEndsATurnItsPromptDidNotOpen(t *testing.T) {
	meta := fmt.Sprintf(`{"type":"user","isMeta":true,"timestamp":"%s","message":{"content":"<task-notification>build done</task-notification>"}}`, stamp(time.Now()))
	answer := fmt.Sprintf(`{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"text","text":"The build is done."}]}}`, stamp(time.Now()))
	apiError := fmt.Sprintf(`{"type":"assistant","timestamp":"%s","isApiErrorMessage":true,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 529 Overloaded"}]}}`, stamp(time.Now()))
	if n := claudeTurns(meta, answer, k1TurnEnd()); n != 1 {
		t.Fatalf("a turn a meta line started: %d turns; want 1", n)
	}
	if n := claudeTurns(claudeUser(time.Now(), "run the build"), apiError, meta, answer, k1TurnEnd()); n != 2 {
		t.Fatalf("an API error, then a turn a meta line started: %d turns; want 2", n)
	}
}
