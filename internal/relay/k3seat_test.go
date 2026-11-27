package relay

// The sixteenth review's seat findings (sr2seat).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Antigravity writes no final step for an interrupted turn: its poll reads
// the status file (idle) and folds a turn end of its own, and the quota
// there as usage news. Those observations come from no transcript line, so
// they start at position 0. A reattached seat whose record holds a read
// mark takes every observation that starts before the mark for history, so
// both are dropped: the turn is never reported, the seat says working
// until it goes quiet for ten minutes, and the usage news is never given.
// On main (no read mark) the same observations, stamped now, are news.
func TestSr2SeatAgyStatusTurnEndAndQuotaAfterAReattachWithAReadMark(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	dir := t.TempDir()
	status := filepath.Join(dir, "agy-status.json")
	transcript := filepath.Join(dir, "transcript_full.jsonl")
	// The history the record has read: one step, ending at 4097.
	if err := os.WriteFile(transcript, []byte(`{"type":"USER_INPUT"`+strings.Repeat(" ", 4096-20)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeStatus := func(state string) {
		t.Helper()
		raw := `{"conversation_id":"` + id + `","agent_state":"` + state + `","model":{"display_name":"Gemini 3.8"},"quota":{"gemini-5h":{"remaining_fraction":0.01,"reset_time":"2026-11-28T00:00:00Z"}}}`
		if err := os.WriteFile(status, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name string
		read int64
	}{{"no read mark on the record (as on main)", 0}, {"the record's read mark", 4097}} {
		t.Run(c.name, func(t *testing.T) {
			writeStatus("running")
			s := attachSeat(Spec{StatusFile: status, Read: c.read}, antigravityAgent{}, &fakeHost{alive: true}, "x", id, transcript, time.Now().Add(-time.Hour))
			s.caughtUp(time.Now()) // the transcript held nothing new
			// News after the reattach, past the mark: a prompt starts a turn.
			now := time.Now()
			s.fold(obs{kind: "user", text: "refactor the parser", at: now, pos: 4097})
			s.fold(obs{kind: "working", at: now, pos: 4097})
			drain(s.events)
			// The user interrupts it: agy writes no last step and says idle.
			writeStatus("idle")
			s.mu.Lock()
			s.lastLine = time.Now().Add(-time.Minute)
			s.mu.Unlock()
			antigravityAgent{}.poll(s, time.Now())
			var turn, idle, limit bool
			for _, ev := range drain(s.events) {
				switch {
				case ev.Kind == KindTurn && ev.Interrupted:
					turn = true
				case ev.Kind == KindState && ev.State == "idle":
					idle = true
				case ev.Kind == KindLimit:
					limit = true
				}
			}
			s.mu.Lock()
			state := s.state
			s.mu.Unlock()
			if !turn || !idle || state != "idle" {
				t.Errorf("after the interrupt: turn reported %v, idle reported %v, seat state %q; want the interrupted turn reported and the seat idle", turn, idle, state)
			}
			if !limit {
				t.Errorf("the status file's quota (99%% used) was never reported")
			}
		})
	}
}

// A reattach with a send the record still waits on reads pi's session
// from the top (restoring), 8 MB a look, and polls pi's status file after
// each look. pi moved to another session while eagent was down (/new at
// its terminal): the first look's poll follows it, and the next look drops
// the session left, which was read only to 8 MB. The send's arrival and
// the turn it started, written after that, are never read: the turn's
// report is lost, and the send, which arrived, is left to be reported
// unconfirmed ("not typed again, so send it again").
func TestSr2SeatPiMoveDuringTheCatchUpOfALongSessionLosesItsEnd(t *testing.T) {
	isolatePi(t)
	for _, mb := range []int{1, 9} { // 1 MB: read in one look, passes
		t.Run(string(rune('0'+mb))+"MB", func(t *testing.T) { sr2PiMoveCatchUp(t, mb) })
	}
}

func sr2PiMoveCatchUp(t *testing.T, mb int) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "second.jsonl")
	status := filepath.Join(dir, "pi-status.json")
	sentAt := time.Now().Add(-time.Minute)
	lines := []string{piHeaderLine("first", sentAt.Add(-2*time.Hour))}
	for i := 0; i < mb; i++ { // older turns, 1 MB each
		lines = append(lines, piTurnLines("old"+string(rune('a'+i)), strings.Repeat("x", 1<<20), "ok", sentAt.Add(-time.Hour+time.Duration(i)*time.Second))...)
	}
	lines = append(lines, piTurnLines("new", "run the tests", "All green.", sentAt.Add(time.Second))...)
	writeLines(t, first, lines...)
	writeLines(t, second, piHeaderLine("second", sentAt.Add(30*time.Second)))
	writePiStatus(t, status, piStatus{State: "idle", SessionFile: second, SessionID: "second", SessionEntries: entries(0)})
	spec := Spec{Dir: dir, StatusFile: status, Restore: []Restored{{ID: "r1", Text: "run the tests", Mode: "queue", At: sentAt}}}
	w := &reattachedSeat{h: &fakeHost{alive: true}, done: make(chan struct{})}
	w.s = attachSeat(spec, piAgent{name: "pi", label: "Pi", binary: "pi"}, w.h, "sr2pi", "first", first, sentAt)
	w.s.poll = piLook
	go w.s.watch()
	go func() {
		defer close(w.done)
		for ev := range w.s.Events() {
			if ev.Kind == KindActivity {
				continue
			}
			w.mu.Lock()
			w.events = append(w.events, ev)
			w.mu.Unlock()
		}
	}()
	t.Cleanup(func() { w.s.Close(); <-w.done })
	// Reading 9 MB to its end takes longer than eventually's 2 s under
	// the race detector.
	if !within(10*time.Second, func() bool { w.s.mu.Lock(); defer w.s.mu.Unlock(); return !w.s.catchingUp }) {
		t.Fatal("the seat never caught up on the transcript")
	}
	w.settle()
	ok, _ := w.delivered()
	if len(ok) != 1 || ok[0] != "r1" {
		t.Errorf("confirmed %v; want r1, whose arrival is in the session pi left", ok)
	}
	if turns := w.turns(); len(turns) != 1 || turns[0] != "All green." {
		t.Errorf("turns reported %q; want the turn the send started", turns)
	}
}

// sr2DialogOnce shows Claude's folder-trust dialog at the first look only.
type sr2DialogOnce struct {
	fakeHost
	looks int
}

func (h *sr2DialogOnce) Capture(string, int) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.looks++
	if h.looks == 1 {
		return "Do you trust the files in this folder?\n❯ 1. Yes, I trust this folder\n  2. No, exit", nil
	}
	return "> \n  ? for shortcuts", nil
}

// 079592e's sibling: Send's last look before typing answers a dialog it
// knows and says so (KindProgress) from Send's goroutine, through emit,
// which puts the watcher's read position on it. The record then marks the
// line the watcher is folding as read before that line's own events are
// recorded (a cut between them makes a reattach take it for history), or
// moves the mark back past lines whose events are already recorded (a
// reattach reports them again).
func TestSr2SeatSendsDialogAnswerCarriesNoReadPosition(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	h := &sr2DialogOnce{fakeHost: fakeHost{alive: true}}
	s := newSeat(Spec{}, claudeAgent{}, h, "sr2dlg", "")
	s.state = "idle"
	s.readTo.Store(4096) // the watcher is folding a line that ends there
	if _, err := s.Send("r1", "run the tests", "queue"); err != nil {
		t.Fatal(err)
	}
	answered := false
	for _, ev := range drain(s.events) {
		if ev.Kind == KindProgress {
			answered = true
			if ev.Read != 0 {
				t.Errorf("Send's dialog answer %q says the transcript was read to %d", ev.Text, ev.Read)
			}
		}
	}
	if !answered {
		t.Fatal("the dialog was not answered")
	}
}

// sr2PasteGate holds a paste until the test lets it go.
type sr2PasteGate struct {
	fakeHost
	entered, release chan struct{}
}

func (h *sr2PasteGate) Paste(target, text string) error {
	h.entered <- struct{}{}
	<-h.release
	return h.fakeHost.Paste(target, text)
}

// The agent's own command (/compact) is confirmed by Send itself once it
// is typed (new on this branch; main emitted nothing from Send). A seat
// closed while the command is being typed (the session quitting, which
// only stops watching, or a stop or restart, whose Close waits for the
// watcher before the pane is killed) has had its events channel closed by
// the watcher; Send's emitAside then selects between a send on that closed
// channel and the closed stop channel, and Go picks either: half the time
// the process panics ("send on closed channel"), taking eagent (or every
// session of eagent serve) down.
func TestSr2SeatCommandTypedWhileTheSeatClosesDoesNotPanic(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	panics := 0
	const tries = 16
	for i := 0; i < tries; i++ {
		h := &sr2PasteGate{fakeHost: fakeHost{alive: true}, entered: make(chan struct{}), release: make(chan struct{})}
		s := newSeat(Spec{}, claudeAgent{}, h, "sr2close", "")
		s.state = "idle"
		go s.watch()
		got := make(chan any, 1)
		go func() {
			defer func() { got <- recover() }()
			_, _ = s.Send("r1", "/compact", "queue")
		}()
		<-h.entered // past Send's closed check, typing
		s.Close()   // returns once the watcher is done: events is closed
		close(h.release)
		if p := <-got; p != nil {
			panics++
			t.Logf("try %d: Send panicked: %v", i, p)
		}
	}
	if panics > 0 {
		t.Errorf("Send panicked in %d of %d closes during the typing of a command", panics, tries)
	}
}

// The suspicion: a reattach's first look cannot read the transcript (here
// it is unreadable for a moment), so the catch-up ends with no history
// read; the next looks read that history, all before the record's read
// mark. What it says (the turn it was in) is no news, and no event it
// gives may say the transcript was read to less than the record's mark:
// that would move the mark back, and a later reattach would report again
// what the record already has.
func TestK3HistoryReadAfterAnEarlyCatchUpDoesNotMoveTheMarkBack(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	since := time.Now().Add(-time.Minute)
	history := []string{
		claudeUser(since.Add(-2*time.Second), "sort the shelves"),
		`{"type":"assistant","timestamp":"` + stamp(since.Add(-time.Second)) + `","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`,
	}
	writeLines(t, path, history...)
	st, _ := os.Stat(path)
	mark := st.Size()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	w := &reattachedSeat{h: &fakeHost{alive: true}, done: make(chan struct{})}
	w.s = attachSeat(Spec{Read: mark}, claudeAgent{}, w.h, "k3-early-"+filepath.Base(dir), "", path, since)
	w.s.poll = piLook
	go w.s.watch()
	go func() {
		defer close(w.done)
		for ev := range w.s.Events() {
			w.mu.Lock()
			w.events = append(w.events, ev)
			w.mu.Unlock()
		}
	}()
	t.Cleanup(func() { w.s.Close(); <-w.done })
	w.caughtUp(t)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	w.settle()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ev := range w.events {
		if ev.Read > 0 && ev.Read < mark {
			t.Errorf("%s event %q says the transcript was read to %d, before the record's mark %d", ev.Kind, ev.State+ev.Text, ev.Read, mark)
		}
	}
}
