package relay

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// enterFails is a terminal whose first Enter is refused (a slow or busy
// tmux), after the paste went in.
type enterFails struct {
	*fakeHost
	failed bool
}

func (h *enterFails) Keys(target string, keys ...string) error {
	if !h.failed && slices.Contains(keys, "Enter") {
		h.failed = true
		return fmt.Errorf("tmux: timed out")
	}
	return h.fakeHost.Keys(target, keys...)
}

// A message held for Antigravity is typed into its native queue. If that
// typing fails after the paste went in, the text is not pasted a second
// time: the send fails and the model decides.
func TestAntigravityHeldMessageIsNeverPastedTwice(t *testing.T) {
	saved := typeSettle
	typeSettle = time.Millisecond
	t.Cleanup(func() { typeSettle = saved })
	h := &enterFails{fakeHost: &fakeHost{alive: true}}
	s := newSeat(Spec{Name: "antigravity"}, antigravityAgent{}, h, "x", "")
	s.state = "working"
	_, err := s.Send("r1", "then run the tests", "after_turn")
	h.mu.Lock()
	typed := slices.Clone(h.typed)
	h.mu.Unlock()
	if len(typed) != 1 {
		t.Fatalf("pasted %d times: %q (err %v)", len(typed), typed, err)
	}
	if err == nil {
		t.Fatal("a send whose Enter was refused reported success")
	}
}

// stamp is a transcript timestamp as pi and Claude write them.
func stamp(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000Z") }

// claudeTurnLines is one Claude Code turn: the user's message, the answer,
// and the turn's end.
func claudeTurnLines(user, reply string, at time.Time) []string {
	return []string{
		fmt.Sprintf(`{"type":"user","timestamp":"%s","message":{"content":%q}}`, stamp(at), user),
		fmt.Sprintf(`{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"text","text":%q}]}}`, stamp(at.Add(time.Millisecond)), reply),
		fmt.Sprintf(`{"type":"system","subtype":"turn_duration","timestamp":"%s","durationMs":2}`, stamp(at.Add(2*time.Millisecond))),
	}
}

// reattachedSeat watches a transcript as Attach does after eagent resumed:
// since is the record's last relay event, restore what the record still
// waits on.
type reattachedSeat struct {
	s      *Seat
	h      *fakeHost
	mu     sync.Mutex
	events []Event
	done   chan struct{}
}

func reattach(t *testing.T, agent Agent, spec Spec, transcript string, since time.Time, restore ...Restored) *reattachedSeat {
	t.Helper()
	spec.Restore = restore
	if _, ok := agent.(piAgent); ok && spec.StatusFile == "" {
		// pi runs with eagent's extension, which keeps a status file (here
		// naming no session, so the seat follows the transcript it has).
		spec.StatusFile = filepath.Join(spec.Dir, "pi-status.json")
		writePiStatus(t, spec.StatusFile, piStatus{State: "idle"})
	}
	w := &reattachedSeat{h: &fakeHost{alive: true}, done: make(chan struct{})}
	w.s = attachSeat(spec, agent, w.h, "r6-"+filepath.Base(filepath.Dir(transcript)), "", transcript, since)
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
	return w
}

// caughtUp waits for the seat to have read its transcript to the end.
func (w *reattachedSeat) caughtUp(t *testing.T) {
	t.Helper()
	if !eventually(func() bool { w.s.mu.Lock(); defer w.s.mu.Unlock(); return !w.s.catchingUp }) {
		t.Fatal("the seat never caught up on the transcript")
	}
}

// delivered lists the confirmations (id) and the reports of a send not
// confirmed (id and detail).
func (w *reattachedSeat) delivered() (ok, failed []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ev := range w.events {
		if ev.Kind == KindDelivered {
			if ev.OK {
				ok = append(ok, ev.SendID)
			} else {
				failed = append(failed, ev.SendID+": "+ev.Detail)
			}
		}
	}
	return ok, failed
}

func (w *reattachedSeat) turns() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, ev := range w.events {
		if ev.Kind == KindTurn {
			out = append(out, ev.Text)
		}
	}
	return out
}

// The reviewer's case, for pi and Claude: a send typed before eagent
// stopped, then the agent's transcript shows it arriving and the turn it
// started finishing while nothing watched. The reattached seat reports
// the turn and confirms the send, once.
func TestAReattachedSeatConfirmsASendThatArrivedWhileEagentWasDown(t *testing.T) {
	isolatePi(t)
	since := time.Now().Add(-time.Minute)
	sent := Restored{ID: "r1", Text: "run the tests and fix what fails", Mode: "queue", At: since.Add(-2 * time.Second)}
	for _, c := range []struct {
		name  string
		agent Agent
		lines []string
	}{
		{"pi", piAgent{label: "Pi"}, append([]string{piHeaderLine("p1", since.Add(-time.Hour)), piModelLine(since.Add(-time.Hour))}, piTurnLines("t1", sent.Text, "All green.", since.Add(time.Second))...)},
		{"claude", claudeAgent{}, claudeTurnLines(sent.Text, "All green.", since.Add(time.Second))},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "t.jsonl")
			writeLines(t, path, c.lines...)
			w := reattach(t, c.agent, Spec{Dir: dir}, path, since, sent)
			w.caughtUp(t)
			if !eventually(func() bool { return len(w.turns()) == 1 }) {
				t.Fatalf("turns %q", w.turns())
			}
			ok, failed := w.delivered()
			if !slices.Equal(ok, []string{"r1"}) || len(failed) > 0 {
				t.Fatalf("confirmed %v, not confirmed %v; want r1 confirmed once", ok, failed)
			}
		})
	}
}

// The transcript line of a send can be older than the record's last
// event: the agent wrote it, then something else of that relay was
// recorded before the seat read the line, then eagent stopped. The
// catch-up confirms it from the history, and reports nothing else of the
// history again.
func TestARestoredSendIsConfirmedByALineFromJustBeforeTheReattach(t *testing.T) {
	isolatePi(t)
	since := time.Now().Add(-time.Minute)
	sent := Restored{ID: "r1", Text: "check the version", Mode: "queue", At: since.Add(-10 * time.Second)}
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	writeLines(t, path, append([]string{piHeaderLine("p1", since.Add(-time.Hour)), piModelLine(since.Add(-time.Hour))}, piTurnLines("t1", sent.Text, "It is 1.6.1.", since.Add(-time.Second))...)...)
	w := reattach(t, piAgent{label: "Pi"}, Spec{Dir: dir}, path, since, sent)
	w.caughtUp(t)
	ok, failed := w.delivered()
	if !slices.Equal(ok, []string{"r1"}) || len(failed) > 0 {
		t.Fatalf("confirmed %v, not confirmed %v", ok, failed)
	}
	if turns := w.turns(); len(turns) > 0 {
		t.Fatalf("the history's turn was reported again: %q", turns)
	}
}

// Two sends of the same words: r1 was confirmed before eagent stopped, r2
// was not. r1's line in the history is not r2's arrival; a line after r2
// was sent is.
func TestAConfirmedSendsLineDoesNotConfirmARestoredOneWithTheSameWords(t *testing.T) {
	isolatePi(t)
	since := time.Now().Add(-time.Minute)
	r2 := Restored{ID: "r2", Text: "continue", Mode: "queue", At: since.Add(-10 * time.Second)}
	head := []string{piHeaderLine("p1", since.Add(-time.Hour)), piModelLine(since.Add(-time.Hour))}
	head = append(head, piTurnLines("t1", "continue", "Step one done.", since.Add(-20*time.Second))...)
	t.Run("not shown", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "t.jsonl")
		writeLines(t, path, head...)
		w := reattach(t, piAgent{label: "Pi"}, Spec{Dir: dir}, path, since, r2)
		w.caughtUp(t)
		if ok, _ := w.delivered(); len(ok) > 0 {
			t.Fatalf("the earlier send's line confirmed %v", ok)
		}
	})
	t.Run("shown after it was sent", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "t.jsonl")
		writeLines(t, path, append(head, piTurnLines("t2", "continue", "Step two done.", since.Add(-5*time.Second))...)...)
		w := reattach(t, piAgent{label: "Pi"}, Spec{Dir: dir}, path, since, r2)
		w.caughtUp(t)
		if ok, _ := w.delivered(); !slices.Equal(ok, []string{"r2"}) {
			t.Fatalf("confirmed %v, want r2", ok)
		}
	})
}

// A send made on the reattached seat is not confirmed by a line from
// before the reattach with the same words: history confirms restored sends
// only.
func TestAHistoryLineDoesNotConfirmASendMadeAfterTheReattach(t *testing.T) {
	t.Parallel()
	since := time.Now().Add(-time.Minute)
	s := attachSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "", "", since)
	s.track("r5", "continue", "queue")
	s.fold(obs{kind: "user", text: "continue", at: since.Add(-time.Second)})
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered {
			t.Fatalf("a history line confirmed %s", ev.SendID)
		}
	}
}

// A restored typed send the transcript never shows: nothing is said of it
// while the seat catches up, and its wait starts when the catch-up ends,
// however long ago it was sent. After the wait a live send gets, it is
// reported not confirmed, saying it was not typed again; no Enter is
// pressed for it, though its words are on the screen.
func TestARestoredSendTheTranscriptNeverShowsIsReportedAfterTheLiveWait(t *testing.T) {
	isolatePi(t)
	since := time.Now().Add(-time.Hour)
	sent := Restored{ID: "r1", Text: "deploy the docs site", Mode: "queue", At: since.Add(-time.Hour)}
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	old := since.Add(-2 * time.Hour)
	writeLines(t, path, append([]string{piHeaderLine("p1", old), piModelLine(old)}, piTurnLines("t1", "something else", "Done.", since.Add(-time.Minute))...)...)
	w := reattach(t, piAgent{label: "Pi"}, Spec{Dir: dir}, path, since, sent)
	w.caughtUp(t)
	w.h.mu.Lock()
	w.h.screen = "> deploy the docs site\n 0.4%/1.0M (auto)"
	w.h.mu.Unlock()
	start := time.Now()
	w.s.mu.Lock()
	w.s.stateAt = start.Add(-time.Minute)
	w.s.mu.Unlock()
	w.s.checkScreen(start.Add(10 * time.Second))
	if _, failed := w.delivered(); len(failed) > 0 {
		t.Fatalf("reported %v ten seconds after the catch-up: the wait counted from when it was sent", failed)
	}
	w.s.checkScreen(start.Add(50 * time.Second))
	if !eventually(func() bool { _, failed := w.delivered(); return len(failed) == 1 }) {
		t.Fatal("never reported")
	}
	ok, failed := w.delivered()
	if len(ok) > 0 || !strings.HasPrefix(failed[0], "r1: ") || !strings.Contains(failed[0], "not typed again") {
		t.Fatalf("confirmed %v, not confirmed %q", ok, failed)
	}
	w.h.mu.Lock()
	defer w.h.mu.Unlock()
	if len(w.h.keys) > 0 || len(w.h.typed) > 0 {
		t.Fatalf("pressed %q, typed %q for a restored send", w.h.keys, w.h.typed)
	}
}

// Messages held for the end of a turn, restored. With their spool file
// still there the hook hands them over later: they stay held, on no timer,
// and the left-over report still takes one whose turn is over. With the
// file gone, the transcript's hand-over confirms them; one it does not show
// is reported after the wait a typed send gets.
func TestRestoredHeldMessages(t *testing.T) {
	isolatePi(t)
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	since := time.Now().Add(-time.Minute)
	header := []string{piHeaderLine("p1", since.Add(-time.Hour)), piModelLine(since.Add(-time.Hour))}
	working := fmt.Sprintf(`{"type":"message","id":"w-u","parentId":null,"timestamp":"%s","message":{"role":"user","content":[{"type":"text","text":"build it"}]}}`, stamp(since.Add(-30*time.Second)))
	held := func(id, text string) Restored {
		return Restored{ID: id, Text: text, Mode: "after_turn", At: since.Add(-20 * time.Second)}
	}
	setup := func(t *testing.T, lines []string) (Spec, string) {
		dir := t.TempDir()
		path := filepath.Join(dir, "t.jsonl")
		writeLines(t, path, lines...)
		return Spec{Dir: dir, Spool: filepath.Join(dir, "after_turn"), Hook: filepath.Join(dir, "ext.ts")}, path
	}
	t.Run("in the spool, mid-turn: stays held", func(t *testing.T) {
		spec, path := setup(t, append(header, working))
		if err := spoolMessage(spec.Spool, "r1", "then run the tests"); err != nil {
			t.Fatal(err)
		}
		w := reattach(t, piAgent{label: "Pi"}, spec, path, since, held("r1", "then run the tests"))
		w.caughtUp(t)
		w.s.mu.Lock()
		w.s.stateAt = time.Now().Add(-time.Minute)
		w.s.mu.Unlock()
		w.s.checkScreen(time.Now().Add(time.Minute))
		if ok, failed := w.delivered(); len(ok)+len(failed) > 0 {
			t.Fatalf("confirmed %v, not confirmed %v", ok, failed)
		}
		if p := w.s.Snapshot().Pending; len(p) != 1 || !strings.HasPrefix(p[0], "r1 (after_turn") {
			t.Fatalf("pending %q", p)
		}
	})
	t.Run("in Claude's spool, turn over: reported left over", func(t *testing.T) {
		// The seat reports left-overs for Claude, whose Stop hook runs only
		// at a turn's end; pi's extension hands them over whenever pi is
		// idle.
		spec, path := setup(t, claudeTurnLines("build it", "Built.", since.Add(-30*time.Second)))
		if err := spoolMessage(spec.Spool, "r1", "then run the tests"); err != nil {
			t.Fatal(err)
		}
		w := reattach(t, claudeAgent{}, spec, path, since, held("r1", "then run the tests"))
		if !eventually(func() bool { _, failed := w.delivered(); return len(failed) == 1 }) {
			t.Fatal("the left-over was never reported")
		}
		if _, failed := w.delivered(); !strings.Contains(failed[0], "turn ended before it took this message") {
			t.Fatalf("reported %q", failed)
		}
	})
	t.Run("handed over while eagent was down: confirmed", func(t *testing.T) {
		spec, path := setup(t, append(header, piTurnLines("t1", "then run the tests\n\nand push", "Pushed.", since.Add(time.Second))...))
		w := reattach(t, piAgent{label: "Pi"}, spec, path, since, held("r1", "then run the tests"), held("r2", "and push"))
		w.caughtUp(t)
		if ok, failed := w.delivered(); !slices.Equal(ok, []string{"r1", "r2"}) || len(failed) > 0 {
			t.Fatalf("confirmed %v, not confirmed %v", ok, failed)
		}
	})
	t.Run("gone and not shown: reported after the wait", func(t *testing.T) {
		spec, path := setup(t, append(header, piTurnLines("t1", "build it", "Built.", since.Add(-30*time.Second))...))
		w := reattach(t, piAgent{label: "Pi"}, spec, path, since, held("r1", "then run the tests"))
		w.caughtUp(t)
		start := time.Now()
		w.s.mu.Lock()
		w.s.stateAt = start.Add(-time.Minute)
		w.s.mu.Unlock()
		w.s.checkScreen(start.Add(10 * time.Second))
		if _, failed := w.delivered(); len(failed) > 0 {
			t.Fatalf("reported %v before the wait", failed)
		}
		w.s.checkScreen(start.Add(50 * time.Second))
		if !eventually(func() bool { _, failed := w.delivered(); return len(failed) == 1 }) {
			t.Fatal("never reported")
		}
	})
}
