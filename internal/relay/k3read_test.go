package relay

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A9. Claude finished turn A, which the record has; then turn B, written
// just before a harness-only event that moved the record's last event time
// past B's lines, and not read before eagent stopped. The record says how
// far the transcript was read (the end of A): a reattach reads B as news
// and reports it, where the cut by time took it for history.
func TestK3ATurnWrittenBeforeAHarnessOnlyEventIsReadOnAReattach(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	at := time.Now().Add(-time.Minute)
	a := strings.Join(claudeTurnLines("build it", "Built it.", at), "\n") + "\n"
	b := strings.Join(claudeTurnLines("then test it", "Tested it.", at.Add(time.Second)), "\n") + "\n"
	if err := os.WriteFile(path, []byte(a+b), 0o644); err != nil {
		t.Fatal(err)
	}
	since := time.Now() // the harness-only event, after B was written
	w := reattach(t, claudeAgent{}, Spec{Dir: dir, Read: int64(len(a))}, path, since)
	w.caughtUp(t)
	if !eventually(func() bool { return slices.Equal(w.turns(), []string{"Tested it."}) }) {
		t.Fatalf("turns reported %q; want B's only", w.turns())
	}
	// A record that does not say how far it read (written before it did)
	// is cut by time as it always was: B is history there (the fault).
	old := reattach(t, claudeAgent{}, Spec{Dir: dir}, path, since)
	old.caughtUp(t)
	if got := old.turns(); len(got) != 0 {
		t.Fatalf("without a recorded read, turns reported %q; want none, as before", got)
	}
}

// A9, a review's case: the agent's own command typed while the watcher is
// folding a line (another goroutine) is confirmed at once; that event is
// no line's, and does not carry how far the watcher has read (the record
// would mark that line read before the line's own events are recorded).
func TestK3AnEventFromASendCarriesNoReadPosition(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "k3cmd", "")
	s.state = "idle"
	s.readTo.Store(4096) // the watcher is folding a line that ends there
	if _, err := s.Send("r1", "/compact", "queue"); err != nil {
		t.Fatal(err)
	}
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && ev.Read != 0 {
			t.Fatalf("the command's confirmation says the transcript was read to %d", ev.Read)
		}
	}
}

// A9, its marks: what a read gives is said with how far the transcript was
// read (the end of the line it came from), and a read whose only news is a
// user line that confirmed nothing is followed by a mark of how far it was
// read, so the record knows that line was read.
func TestK3AEventsSayHowFarTheTranscriptWasRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	turn := strings.Join(claudeTurnLines("build it", "Built it.", time.Now()), "\n") + "\n"
	if err := os.WriteFile(path, []byte(turn), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.state = "working"
	tail, p := &tailer{path: path}, s.agent.newParser(s.spec)
	if err := s.readTail(tail, p, time.Now()); err != nil {
		t.Fatal(err)
	}
	var turnRead int64
	for _, ev := range drain(s.events) {
		if ev.Kind == KindTurn {
			turnRead = ev.Read
		}
	}
	if turnRead != int64(len(turn)) {
		t.Fatalf("the turn says it was read to %d; want %d (the end of its line)", turnRead, len(turn))
	}
	more := claudeUser(time.Now(), "words typed in the terminal") + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(more); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := s.readTail(tail, p, time.Now()); err != nil {
		t.Fatal(err)
	}
	var read int64
	for _, ev := range drain(s.events) {
		if ev.Kind == KindRead {
			read = ev.Read
		}
	}
	if want := int64(len(turn) + len(more)); read != want {
		t.Fatalf("the silent user line's read is marked %d; want %d", read, want)
	}
}
