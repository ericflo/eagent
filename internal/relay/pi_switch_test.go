package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// When pi moves to another session of its own (/new, /resume, /fork, or an
// extension's ctx.newSession), the seat follows it. What pi wrote in the
// new session before the watcher noticed the switch is news; what the
// session already held when pi opened it is history.

// piLines builds a run of pi session lines: a header, then one complete
// turn (a user message, the reply, the extension's mark), stamped at.
func piTurnLines(id, user, reply string, at time.Time) []string {
	ts := func(d time.Duration) string { return at.Add(d).UTC().Format("2006-01-02T15:04:05.000Z") }
	return []string{
		fmt.Sprintf(`{"type":"message","id":"%s-u","parentId":null,"timestamp":"%s","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, id, ts(0), user),
		fmt.Sprintf(`{"type":"message","id":"%s-a","parentId":"%s-u","timestamp":"%s","message":{"role":"assistant","content":[{"type":"text","text":%q}],"stopReason":"stop"}}`, id, id, ts(2*time.Millisecond), reply),
		fmt.Sprintf(`{"type":"custom","id":"%s-s","parentId":"%s-a","timestamp":"%s","customType":"eagent-settled","data":{"aborted":false}}`, id, id, ts(3*time.Millisecond)),
	}
}

func piHeaderLine(id string, at time.Time) string {
	return fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":"%s","cwd":"/work/project"}`, id, at.UTC().Format("2006-01-02T15:04:05.000Z"))
}

func piModelLine(at time.Time) string {
	return fmt.Sprintf(`{"type":"model_change","id":"m1","parentId":null,"timestamp":"%s","provider":"local","modelId":"m"}`, at.UTC().Format("2006-01-02T15:04:05.000Z"))
}

func writeLines(t *testing.T, file string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendLines(t *testing.T, file string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func entries(n int) *int { return &n }

// piLook is how often the watched pi seats in these tests look: pollEvery
// (700 ms) in production. What the tests wait out is a number of looks, so
// a quiet window spans a dozen looks where it spanned two or three.
const piLook = 25 * time.Millisecond

// looks is n of the watcher's looks.
func looks(n int) time.Duration { return time.Duration(n) * piLook }

// quietLooks is how many looks a test waits for something not to happen.
const quietLooks = 12

// piSwitchSeat watches a pi seat whose status file names first (session
// id "first") and waits until it follows it.
type piSwitchSeat struct {
	t      *testing.T
	s      *Seat
	dir    string
	status string
	first  string
}

// isolatePi gives a test and its subtests a pi home of their own. The
// seat helpers leave it to the top-level test, so that its subtests can
// run in parallel (a test that sets the environment cannot).
func isolatePi(t *testing.T) {
	t.Helper()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
}

// newPiSwitchSeat starts the seat. The caller's top-level test has called
// isolatePi.
func newPiSwitchSeat(t *testing.T, since time.Time, firstLines []string, firstEntries *int) *piSwitchSeat {
	t.Helper()
	dir := t.TempDir()
	w := &piSwitchSeat{t: t, dir: dir, status: filepath.Join(dir, "pi-status.json"), first: filepath.Join(dir, "first.jsonl")}
	writeLines(t, w.first, firstLines...)
	writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: w.first, SessionID: "first", SessionEntries: firstEntries})
	w.s = newSeat(Spec{StatusFile: w.status, Dir: dir}, piAgent{label: "Pi"}, &fakeHost{alive: true}, "pi-switch-"+filepath.Base(dir), "first")
	w.s.state = "idle"
	w.s.since = since
	w.s.poll = piLook
	go w.s.watch()
	t.Cleanup(w.s.Close)
	return w
}

// until collects events until one matches, failing after limit.
func (w *piSwitchSeat) until(limit time.Duration, match func(Event) bool) []Event {
	w.t.Helper()
	var got []Event
	deadline := time.After(limit)
	for {
		select {
		case ev, ok := <-w.s.events:
			if !ok {
				w.t.Fatalf("events closed; saw %+v", got)
			}
			if ev.Kind == KindActivity {
				continue
			}
			got = append(got, ev)
			if match(ev) {
				return got
			}
		case <-deadline:
			w.t.Fatalf("timed out; saw %+v", got)
		}
	}
}

// quiet collects what arrives within d.
func (w *piSwitchSeat) quiet(d time.Duration) []Event {
	var got []Event
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-w.s.events:
			if !ok {
				return got
			}
			if ev.Kind != KindActivity {
				got = append(got, ev)
			}
		case <-deadline:
			return got
		}
	}
}

func innerIs(path string) func(Event) bool {
	return func(ev Event) bool { return ev.Kind == KindInner && ev.Transcript == path }
}

func turnsIn(evs []Event) []string {
	var out []string
	for _, ev := range evs {
		if ev.Kind == KindTurn {
			out = append(out, ev.Text)
		}
	}
	return out
}

func isTurn(ev Event) bool { return ev.Kind == KindTurn }

// The reviewer's case: an extension calls ctx.newSession and sends a
// kickoff from withSession; the whole first turn of the new session is on
// disk before the watcher's next poll sees the switch. It is reported, once.
func TestPiSwitchReportsAFastFirstTurn(t *testing.T) {
	isolatePi(t)
	for _, tc := range []struct {
		name    string
		entries *int // what the status file says the session held at its start
	}{
		{"extension records the start", entries(0)},
		{"older extension without the count", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			now := time.Now()
			w := newPiSwitchSeat(t, time.Time{}, []string{piHeaderLine("first", now)}, entries(0))
			w.until(5*time.Second, innerIs(w.first))
			second := filepath.Join(w.dir, "second.jsonl")
			// pi writes the new file and its whole first turn...
			lines := append([]string{piHeaderLine("second", time.Now()), piModelLine(time.Now())}, piTurnLines("k", "Continue in the replacement session", "Kickoff done.", time.Now())...)
			writeLines(t, second, lines...)
			// ...and the status file names it, before any poll looks.
			writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: second, SessionID: "second", Settled: 1, SessionEntries: tc.entries})
			got := w.until(5*time.Second, isTurn)
			if !innerSeen(got, second, "second") {
				t.Fatalf("did not announce the new session: %+v", got)
			}
			if turns := turnsIn(got); len(turns) != 1 || turns[0] != "Kickoff done." {
				t.Fatalf("turns %q", turns)
			}
			if !deliveredOrUser(got) {
				t.Fatalf("the kickoff did not make pi look working: %+v", got)
			}
			// The status file rewritten for the same session says nothing new.
			for i := 0; i < 3; i++ {
				writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: second, SessionID: "second", Settled: 1, SessionEntries: tc.entries, At: time.Now().String()})
				time.Sleep(looks(4))
			}
			if again := w.quiet(looks(quietLooks)); len(turnsIn(again)) != 0 || hasInner(again) {
				t.Fatalf("reported again: %+v", again)
			}
		})
	}
}

// pi writes a new session's file only once it holds a message: the status
// file can name it before it exists. The first turn still comes through.
func TestPiSwitchBeforeTheFileExists(t *testing.T) {
	isolatePi(t)
	w := newPiSwitchSeat(t, time.Time{}, []string{piHeaderLine("first", time.Now())}, entries(0))
	w.until(5*time.Second, innerIs(w.first))
	second := filepath.Join(w.dir, "second.jsonl")
	writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: second, SessionID: "second", SessionEntries: entries(1)})
	time.Sleep(looks(8))
	writeLines(t, second, append([]string{piHeaderLine("second", time.Now()), piModelLine(time.Now())}, piTurnLines("k", "go", "First reply.", time.Now())...)...)
	got := w.until(5*time.Second, isTurn)
	if turns := turnsIn(got); len(turns) != 1 || turns[0] != "First reply." || !innerSeen(got, second, "second") {
		t.Fatalf("events %+v", got)
	}
	if again := w.quiet(looks(quietLooks)); len(turnsIn(again)) != 0 {
		t.Fatalf("reported again: %+v", again)
	}
}

// /resume of an older conversation, or a fork: the session pi opens already
// holds finished turns. They stay quiet; the next turn is reported.
func TestPiSwitchKeepsHistoryQuiet(t *testing.T) {
	isolatePi(t)
	for _, tc := range []struct {
		name    string
		entries *int
		oldAt   time.Time
	}{
		// A fork copies the turns it keeps, timestamps and all, and can come
		// right after a run settled: only the count tells them apart.
		{"fork just after a turn, extension records the start", entries(7), time.Now()},
		{"resume of an older conversation, extension records the start", entries(7), time.Now().Add(-3 * time.Hour)},
		{"resume of an older conversation, older extension", nil, time.Now().Add(-3 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newPiSwitchSeat(t, time.Time{}, []string{piHeaderLine("first", time.Now())}, entries(0))
			w.until(5*time.Second, innerIs(w.first))
			older := filepath.Join(w.dir, "older.jsonl")
			lines := []string{piHeaderLine("older", tc.oldAt), piModelLine(tc.oldAt)}
			lines = append(lines, piTurnLines("o1", "old question", "Old answer one.", tc.oldAt.Add(time.Millisecond))...)
			lines = append(lines, piTurnLines("o2", "another", "Old answer two.", tc.oldAt.Add(10*time.Millisecond))...)
			writeLines(t, older, lines...)
			writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: older, SessionID: "older", SessionEntries: tc.entries})
			got := w.until(5*time.Second, innerIs(older))
			got = append(got, w.quiet(looks(quietLooks))...)
			if turns := turnsIn(got); len(turns) != 0 {
				t.Fatalf("history reported as new: %q", turns)
			}
			appendLines(t, older, piTurnLines("n1", "new question", "New answer.", time.Now())...)
			got = w.until(5*time.Second, isTurn)
			if turns := turnsIn(got); len(turns) != 1 || turns[0] != "New answer." {
				t.Fatalf("turns %q", turns)
			}
			if again := w.quiet(looks(quietLooks)); len(turnsIn(again)) != 0 {
				t.Fatalf("reported again: %+v", again)
			}
		})
	}
}

// A normal launch reads its session from the top; an eagent-side resume
// (since set at launch) leaves the history quiet. Neither changes.
func TestPiLaunchAndResumeUnchanged(t *testing.T) {
	isolatePi(t)
	t.Run("launch", func(t *testing.T) {
		t.Parallel()
		now := time.Now()
		w := newPiSwitchSeat(t, time.Time{}, append([]string{piHeaderLine("first", now), piModelLine(now)}, piTurnLines("f", "hello", "Hi there.", now)...), entries(0))
		got := w.until(5*time.Second, isTurn)
		if turns := turnsIn(got); len(turns) != 1 || turns[0] != "Hi there." || !innerSeen(got, w.first, "first") {
			t.Fatalf("events %+v", got)
		}
		if again := w.quiet(looks(quietLooks)); len(turnsIn(again)) != 0 {
			t.Fatalf("reported again: %+v", again)
		}
	})
	t.Run("resume", func(t *testing.T) {
		t.Parallel()
		old := time.Now().Add(-time.Hour)
		lines := append([]string{piHeaderLine("first", old), piModelLine(old)}, piTurnLines("f", "hello", "Old reply.", old)...)
		// The resumed session's status counts its history, and since is
		// the launch: both keep the history quiet.
		w := newPiSwitchSeat(t, time.Now(), lines, entries(4))
		got := w.until(5*time.Second, innerIs(w.first))
		got = append(got, w.quiet(looks(quietLooks))...)
		if turns := turnsIn(got); len(turns) != 0 {
			t.Fatalf("history reported: %q", turns)
		}
		appendLines(t, w.first, piTurnLines("g", "next", "New reply.", time.Now())...)
		got = w.until(5*time.Second, isTurn)
		if turns := turnsIn(got); len(turns) != 1 || turns[0] != "New reply." {
			t.Fatalf("turns %q", turns)
		}
	})
}

// Back and forth: /resume to the first session again after a switch. Its
// turns were reported once already and are not reported again.
func TestPiSwitchBackDoesNotRepeat(t *testing.T) {
	isolatePi(t)
	now := time.Now()
	w := newPiSwitchSeat(t, time.Time{}, append([]string{piHeaderLine("first", now)}, piTurnLines("f", "hello", "First turn.", now)...), entries(0))
	if turns := turnsIn(w.until(5*time.Second, isTurn)); len(turns) != 1 || turns[0] != "First turn." {
		t.Fatalf("turns %q", turns)
	}
	second := filepath.Join(w.dir, "second.jsonl")
	writeLines(t, second, append([]string{piHeaderLine("second", time.Now())}, piTurnLines("s", "go", "Second session turn.", time.Now())...)...)
	writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: second, SessionID: "second", SessionEntries: entries(0)})
	if turns := turnsIn(w.until(5*time.Second, isTurn)); len(turns) != 1 || turns[0] != "Second session turn." {
		t.Fatalf("turns %q", turns)
	}
	// Back to the first, which pi loads with its 3 entries.
	writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: w.first, SessionID: "first", SessionEntries: entries(3)})
	got := w.until(5*time.Second, innerIs(w.first))
	got = append(got, w.quiet(looks(quietLooks))...)
	if turns := turnsIn(got); len(turns) != 0 {
		t.Fatalf("repeated: %q", turns)
	}
}

func innerSeen(evs []Event, path, id string) bool {
	for _, ev := range evs {
		if ev.Kind == KindInner && ev.Transcript == path && ev.InnerID == id {
			return true
		}
	}
	return false
}

func hasInner(evs []Event) bool {
	for _, ev := range evs {
		if ev.Kind == KindInner {
			return true
		}
	}
	return false
}

func deliveredOrUser(evs []Event) bool {
	for _, ev := range evs {
		if ev.Kind == KindState && ev.State == "working" {
			return true
		}
	}
	return false
}
