package relay

// The coordinator's seat review tests (sr_seat_test.go), copied.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// srLastRead is the read mark the record keeps after these events: the
// newest one that says how far the transcript was read.
func srLastRead(evs []Event) int64 {
	var read int64
	for _, ev := range evs {
		if ev.Read > 0 {
			read = ev.Read
		}
	}
	return read
}

// The read mark says "the end of that line", but the tailer's offset also
// counts the bytes of a last line still being written (kept in rest). The
// line being written then starts before the mark, so a reattach takes it
// for history: here Claude's turn end, whose report is never given.
func TestSrSeatReadMarkStopsAtTheLastWholeLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	at := time.Now().Add(-time.Minute)
	a := strings.Join(claudeTurnLines("build it", "Built it.", at), "\n") + "\n"
	b := claudeTurnLines("then test it", "Tested it.", at.Add(time.Second))
	whole := b[0] + "\n" + b[1] + "\n" // B's prompt and answer, written whole
	end := b[2] + "\n"                 // B's turn_duration, half written at the read
	if err := os.WriteFile(path, []byte(a+whole+end[:len(end)/2]), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.state = "working"
	tail, p := &tailer{path: path}, s.agent.newParser(s.spec)
	if err := s.readTail(tail, p, time.Now()); err != nil {
		t.Fatal(err)
	}
	read := srLastRead(drain(s.events))
	if limit := int64(len(a) + len(whole)); read > limit {
		t.Errorf("the record's read mark is %d, past the last whole line read (%d): it lies inside a line not read yet", read, limit)
	}
	// The line is finished; eagent had stopped. The reattach is told the
	// mark the record holds.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(end[len(end)/2:]); err != nil {
		t.Fatal(err)
	}
	f.Close()
	w := reattach(t, claudeAgent{}, Spec{Dir: dir, Read: read}, path, time.Now())
	w.caughtUp(t)
	if !eventually(func() bool { return slices.Equal(w.turns(), []string{"Tested it."}) }) {
		t.Fatalf("turns reported after the reattach %q; want B's, which no eagent reported", w.turns())
	}
}

// The same with Codex, whose task_complete line carries the whole final
// answer (a long line, the likeliest to be read while still being
// written): the turn and its report are lost on the reattach.
func TestSrSeatCodexReportHalfWrittenAtTheReadIsLostOnAReattach(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	ts := func(at time.Time) string { return `"timestamp":"` + stamp(at) + `"` }
	at := time.Now().Add(-time.Minute)
	turn := func(user, reply string, at time.Time) []string {
		return []string{
			`{` + ts(at) + `,"type":"event_msg","payload":{"type":"task_started"}}`,
			`{` + ts(at) + `,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"text":"` + user + `"}]}}}`,
			`{` + ts(at.Add(time.Second)) + `,"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"` + reply + `","duration_ms":1000}}`,
		}
	}
	a := strings.Join(turn("build it", "Built it.", at), "\n") + "\n"
	b := turn("then test it", "Tested it: "+strings.Repeat("all green. ", 200), at.Add(5*time.Second))
	whole := b[0] + "\n" + b[1] + "\n"
	end := b[2] + "\n"
	if err := os.WriteFile(path, []byte(a+whole+end[:len(end)/2]), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSeat(Spec{}, codexAgent{}, &fakeHost{alive: true}, "x", "")
	s.state = "working"
	tail, p := &tailer{path: path}, s.agent.newParser(s.spec)
	if err := s.readTail(tail, p, time.Now()); err != nil {
		t.Fatal(err)
	}
	read := srLastRead(drain(s.events))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(end[len(end)/2:]); err != nil {
		t.Fatal(err)
	}
	f.Close()
	w := reattach(t, codexAgent{}, Spec{Dir: dir, Read: read}, path, time.Now())
	w.caughtUp(t)
	if !eventually(func() bool { return len(w.turns()) == 1 }) {
		t.Fatalf("read mark %d (the last whole line ends at %d); turns reported after the reattach: %d, want B's", read, len(a)+len(whole), len(w.turns()))
	}
}

// A reattach told the record's read mark takes every line after it for
// news, and news confirms a restored send with no look at when the line
// was written. A line typed at the terminal just before eagent's send of
// the same words was recorded (and not read before eagent stopped) then
// confirms that send, which never went in. A live seat (pending.after) and
// the cut by time (a restored send is confirmed only by a line stamped no
// earlier than it) both refuse it.
func TestSrSeatRestoredSendIsNotConfirmedByALineWrittenBeforeIt(t *testing.T) {
	t.Skip("known, not fixed: fixed in the next commit")
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	at := time.Now().Add(-time.Minute)
	a := strings.Join(claudeTurnLines("build it", "Built it.", at), "\n") + "\n"
	typed := at.Add(10 * time.Second)
	u := strings.Join(claudeTurnLines("run the tests", "Ran them.", typed), "\n") + "\n"
	if err := os.WriteFile(path, []byte(a+u), 0o644); err != nil {
		t.Fatal(err)
	}
	// eagent's send of the same words, recorded half a second after the
	// person's line; eagent stopped before typing it.
	sent := typed.Add(500 * time.Millisecond)
	restore := Restored{ID: "r1", Text: "run the tests", Mode: "queue", At: sent}

	old := reattach(t, claudeAgent{}, Spec{Dir: dir}, path, sent, restore)
	old.caughtUp(t)
	old.settle()
	if ok, _ := old.delivered(); slices.Contains(ok, "r1") {
		t.Fatalf("cut by time, the send was confirmed (%q); the control is wrong", ok)
	}

	w := reattach(t, claudeAgent{}, Spec{Dir: dir, Read: int64(len(a))}, path, sent, restore)
	w.caughtUp(t)
	if !eventually(func() bool { return slices.Equal(w.turns(), []string{"Ran them."}) }) {
		t.Fatalf("turns %q; want the unread turn reported", w.turns())
	}
	if ok, _ := w.delivered(); slices.Contains(ok, "r1") {
		t.Fatalf("the restored send r1 was confirmed by a line written before it was sent (confirmed %q)", ok)
	}
}
