package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// k2ClosingClaude is Claude whose seat is closed at the watcher's first
// look (its poll), and which counts the looks.
type k2ClosingClaude struct {
	claudeAgent
	looks atomic.Int32
}

func (a *k2ClosingClaude) poll(s *Seat, _ time.Time) {
	if a.looks.Add(1) == 1 {
		go s.Close()
		<-s.stop
	}
}

// A catch-up reads chunk after chunk without waiting for the next look
// (c342211); a seat closed meanwhile stops at the next chunk, not at the
// end of a long transcript.
func TestK2ACatchUpStopsWhenTheSeatCloses(t *testing.T) {
	since := time.Now()
	// Lines no parser reads (cheap to pass over), a megabyte each.
	line := strings.Repeat("x", 1<<20)
	var lines []string
	for i := 0; i < 17; i++ {
		lines = append(lines, line)
	}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeLines(t, path, lines...)
	if st, _ := os.Stat(path); st.Size() < 16<<20 {
		t.Fatalf("the transcript is %d bytes; want over two of the tailer's 8 MB reads", st.Size())
	}
	a := &k2ClosingClaude{}
	s := attachSeat(Spec{Restore: []Restored{{ID: "r1", Text: "never sent", Mode: "queue", At: since.Add(-2 * time.Hour)}}}, a, &fakeHost{alive: true}, "k2-stop-"+filepath.Base(filepath.Dir(path)), "", path, since)
	go s.watch()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher did not stop")
	}
	if n := a.looks.Load(); n != 1 {
		t.Fatalf("the watcher looked %d times after the seat was closed at its first look; want 1", n)
	}
}

// The seat says when a dialog the agent reports holds it blocked: its
// status and the blocked state event carry the dialog's title, and
// nothing else does (blocked for a slow start or a quiet turn, or the
// state after the dialog ends).
func TestK2ASeatSaysWhenADialogHoldsIt(t *testing.T) {
	s := newSeat(Spec{}, piAgent{name: "pi", label: "Pi", binary: "pi"}, &fakeHost{alive: true}, "k2-dialog", "")
	s.state = "working"
	next := func() Event {
		for _, ev := range drain(s.events) {
			if ev.Kind == KindState {
				return ev
			}
		}
		t.Fatal("no state event")
		return Event{}
	}
	s.reportDialog("Allow rm -rf build?")
	if ev, st := next(), s.Snapshot(); ev.State != "blocked" || ev.Dialog != "Allow rm -rf build?" || st.Dialog != "Allow rm -rf build?" {
		t.Fatalf("under the dialog: event %+v, status dialog %q", ev, st.Dialog)
	}
	s.reportDialog("")
	if ev, st := next(), s.Snapshot(); ev.State != "working" || ev.Dialog != "" || st.Dialog != "" {
		t.Fatalf("after the dialog: event %+v, status dialog %q", ev, st.Dialog)
	}
	s.setState("blocked", "no transcript activity for 10m; the screen ends:\nbuilding")
	if ev, st := next(), s.Snapshot(); ev.Dialog != "" || st.Dialog != "" {
		t.Fatalf("blocked for a quiet turn: event %+v, status dialog %q", ev, st.Dialog)
	}
}
