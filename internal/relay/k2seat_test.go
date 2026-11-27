package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A12: a held message pi's extension took out of the spool and never ran
// (an interrupt put it back in pi's editor) is lost once the turn is over,
// also when a pi dialog opened within the grace after that turn end: the
// dialog holds the seat while it is up, and the turn end stands after it.
// Lost, it is reported unconfirmed after the usual wait; never typed again.
func TestK2APiDialogAfterATurnEndKeepsThatTurnEnd(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "pi", Spool: spool}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "k2-a12", "")
	s.state = "working"
	if _, err := s.Send("r1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.fold(obs{kind: "aborted"})
	s.reportDialog("Allow rm -rf build?") // opens within the grace
	names := spoolNames(spool)
	for _, n := range names {
		if err := os.Remove(filepath.Join(spool, n)); err != nil { // the extension removed it
			t.Fatal(err)
		}
	}
	s.reportDialog("")
	now := time.Now()
	s.leftOvers(now.Add(heldGrace), false)
	s.mu.Lock()
	lost := len(s.pending) == 1 && s.pending[0].lost
	s.stateAt = now.Add(-time.Hour)
	s.mu.Unlock()
	if !lost {
		t.Fatalf("the held message whose file is gone is not lost (pending %v, spool had %v)", s.Snapshot().Pending, names)
	}
	s.checkScreen(now.Add(time.Hour))
	var got []string
	for _, ev := range deliveries(drain(s.events)) {
		got = append(got, ev.SendID+":"+ev.Outcome)
	}
	if !slices.Equal(got, []string{"r1:" + OutcomeUnconfirmed}) || len(k1Typed(h)) != 0 {
		t.Fatalf("reported %v, typed %q; want r1 unconfirmed, nothing typed", got, k1Typed(h))
	}
}

// A13: eagent was down while the agent's turn came to a dialog the seat
// does not know (a permission prompt) and stayed there. The reattached seat
// reads a history that ends inside that turn, its transcript quiet for
// longer than a live seat waits before calling a working agent blocked: it
// is blocked as the live seat would be by then (hung), so a send goes only
// while the screen shows the agent's own prompt or busy line; into the
// dialog it types nothing. A turn whose transcript moved recently is still
// working.
func TestK2AReattachedSeatQuietInATurnTypesNothingIntoADialog(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	since := time.Now().Add(-15 * time.Minute)
	at := since.Add(-time.Minute)
	claude := []string{
		claudeUser(at, "run the build"),
		fmt.Sprintf(`{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"make build"}}]}}`, stamp(at.Add(time.Second))),
	}
	codex := []string{
		fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"thread-1","cwd":"/w","timestamp":"%s"}}`, stamp(at), stamp(at)),
		fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"task_started"}}`, stamp(at)),
		fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["bash","-lc","make build"],"parsed_cmd":[{"cmd":"make build"}]}}}`, stamp(at.Add(time.Second))),
	}
	dialog := "Do you want to proceed?\n❯ 1. Yes\n  2. No, and tell the agent what to do differently"
	for _, c := range []struct {
		name   string
		agent  Agent
		lines  []string
		quiet  time.Duration // since the transcript was last written
		record string
		want   string // the state the catch-up says
	}{
		{"claude", claudeAgent{}, claude, 14 * time.Minute, "working", "blocked"},
		{"claude_record_idle", claudeAgent{}, claude, 14 * time.Minute, "idle", "blocked"},
		{"codex", codexAgent{}, codex, 14 * time.Minute, "working", "blocked"},
		{"claude_moved_lately", claudeAgent{}, claude, time.Minute, "working", "working"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			writeLines(t, path, c.lines...)
			old := time.Now().Add(-c.quiet)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			h := &fakeHost{alive: true, screen: dialog}
			s := attachSeat(Spec{State: c.record}, c.agent, h, "k2-a13-"+c.name, "thread-1", path, since)
			s.poll = time.Millisecond
			go s.watch()
			t.Cleanup(s.Close)
			ev := nextState(t, s.events)
			if c.want == "blocked" {
				msg, err := s.Send("r1", "also run the tests", "queue")
				if typed := k1Typed(h); len(typed) != 0 || err == nil {
					t.Fatalf("typed %q into the dialog (Send: %q, %v)", typed, msg, err)
				}
			}
			if ev.State != c.want {
				t.Fatalf("the catch-up said %q; want %q", ev.State, c.want)
			}
		})
	}
}

// A1: pi's own pickers (/model, /resume, /tree, /login) open in place of
// its editor and say nothing to an extension: pi's showSelector does not
// go through the extension runner's UI wrapper (no ui_prompt_start), the
// built-in commands never reach the input hook, and only /login and an
// extension's dialogs report "blocked", as OSC 7501 to the terminal, which
// tmux keeps no copy of. So pi's status says idle, and a send is pasted
// into the picker and its Enter picks something.
func TestK2ASendIsNotTypedIntoPisOwnPicker(t *testing.T) {
	t.Skip("known, not fixed: pi 1.6.1 tells an extension nothing when its own pickers open (see the comment); only a screen reading of each picker could, and pi draws each one differently")
	isolatePi(t)
	status := filepath.Join(t.TempDir(), "pi-status.json")
	writePiStatus(t, status, piStatus{State: "idle"})
	h := &fakeHost{alive: true, screen: "Select model\n→ anthropic/claude-sonnet\n  openai/gpt-5\n0.4%/1.0M (auto)"}
	s := newSeat(Spec{StatusFile: status}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "k2-a1", "")
	s.state = "idle"
	if _, err := s.Send("r1", "run the tests", "queue"); err == nil || len(k1Typed(h)) != 0 {
		t.Fatalf("typed %q into pi's model picker", k1Typed(h))
	}
}

// A3: Claude gives a turn up on an API error by writing its synthetic error
// answer, and no turn_duration (Claude Code writes one only for a turn that
// completed), and its Stop hook does not run. That answer is the turn's
// end, as pi's and Antigravity's errors are: the seat says the turn ended
// (interrupted, with the error) and is idle, and a message held for the
// end of the turn, which nothing will hand over now, is reported not sent
// after the usual grace rather than held until some later turn completes.
func TestK2AClaudesAPIErrorEndsItsTurn(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "after_turn")
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, &fakeHost{alive: true}, "k2-a3", "")
	s.state = "working"
	if _, err := s.Send("r1", "then commit the fix", "after_turn"); err != nil {
		t.Fatal(err)
	}
	p := &claudeParser{}
	for _, l := range []string{
		claudeUser(time.Now(), "run the build"),
		fmt.Sprintf(`{"type":"assistant","timestamp":"%s","isApiErrorMessage":true,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 529 Overloaded"}]}}`, stamp(time.Now())),
	} {
		for _, o := range p.line([]byte(l)) {
			s.fold(o)
		}
	}
	s.reportLeftOvers(time.Now().Add(heldGrace))
	var turns, got []string
	for _, ev := range drain(s.events) {
		switch {
		case ev.Kind == KindTurn:
			turns = append(turns, fmt.Sprintf("%v:%s", ev.Interrupted, ev.Text))
		case ev.Kind == KindDelivered:
			got = append(got, ev.SendID+":"+ev.Outcome)
		}
	}
	if !slices.Equal(got, []string{"r1:" + OutcomeNotSent}) || !slices.Equal(turns, []string{"true:API Error: 529 Overloaded"}) || s.Snapshot().State != "idle" {
		t.Fatalf("turns %v, reported %v, state %s; want the turn ended on the error, r1 not sent, idle (spool %v)", turns, got, s.Snapshot().State, spoolNames(spool))
	}
}

// A6, the agent's exit: a message still waiting unclaimed in the spool
// when the agent's pane is gone certainly did not go in, and is reported
// not sent before the seat says exited (so the exit does not call it a
// send that may have gone). One a hook had claimed is left as it was.
func TestK2AHeldMessageInTheSpoolAtAnExitIsNotSent(t *testing.T) {
	saved := aliveEvery
	aliveEvery = time.Millisecond
	t.Cleanup(func() { aliveEvery = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "k2-a6x", "")
	s.state = "working"
	for _, id := range []string{"r1", "r2"} {
		if _, err := s.Send(id, "then "+id, "after_turn"); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range spoolNames(spool) {
		if strings.HasSuffix(n, "-r2.txt") { // a hook claimed r2
			if err := os.Rename(filepath.Join(spool, n), filepath.Join(spool, n+".taken")); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.poll = time.Millisecond
	h.mu.Lock()
	h.alive = false
	h.mu.Unlock()
	go s.watch()
	t.Cleanup(s.Close)
	var got []string
	for ev := range s.events {
		if ev.Kind == KindDelivered {
			got = append(got, ev.SendID+":"+ev.Outcome)
		}
		if ev.Kind == KindState && ev.State == "exited" {
			break
		}
	}
	if !slices.Equal(got, []string{"r1:" + OutcomeNotSent}) {
		t.Fatalf("before the exit: %v; want r1 not sent", got)
	}
}

// A6, a restored send: its arrival may lie anywhere after it was recorded,
// so a reattach with sends the record still waits on reads the whole
// transcript, not only its end, and finds one whose prompt is older than
// that end.
func TestK2ARestoredSendOlderThanTheTailIsConfirmed(t *testing.T) {
	lowTail(t)
	since := time.Now().Add(-time.Minute)
	first := "label the jars on the top shelf"
	path := longHistory(t, since, first)
	w := reattach(t, claudeAgent{}, Spec{Dir: filepath.Dir(path)}, path, since, Restored{ID: "r1", Text: first, Mode: "queue", At: since.Add(-time.Hour - time.Second)})
	w.caughtUp(t)
	if ok, failed := w.delivered(); !slices.Equal(ok, []string{"r1"}) || len(failed) != 0 {
		t.Fatalf("confirmed %v, not confirmed %v; want r1 confirmed", ok, failed)
	}
}

// k2PausedPi is pi whose seat's watcher waits for the test after each poll
// (which comes right after the look's read of the transcript).
type k2PausedPi struct {
	piAgent
	read, more chan struct{}
}

func (a *k2PausedPi) poll(s *Seat, now time.Time) {
	a.piAgent.poll(s, now)
	select {
	case a.read <- struct{}{}:
	case <-s.stop:
		return
	}
	select {
	case <-a.more:
	case <-s.stop:
	}
}

func (a *k2PausedPi) look(t *testing.T) {
	t.Helper()
	select {
	case a.more <- struct{}{}:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher is not waiting")
	}
	select {
	case <-a.read:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher did not look")
	}
}

// A4: with an extension too old to record how many entries a session held
// when pi opened it, news in a session pi moved to is what was written
// after pi was last seen on the old one. A fork copies the turns it keeps
// with their own stamps: a turn that settled between that sighting and
// the look that saw the move was read from the old session, and its copy
// in the fork is no news. A turn pi runs in the fork is.
func TestK2APiTurnJustBeforeAForkIsReportedOnce(t *testing.T) {
	isolatePi(t)
	dir := t.TempDir()
	first, forked, status := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "forked.jsonl"), filepath.Join(dir, "pi-status.json")
	old := time.Now().Add(-time.Hour)
	writeLines(t, first, append([]string{piHeaderLine("first", old)}, piTurnLines("t0", "sort the shelves", "Sorted.", old)...)...)
	writePiStatus(t, status, piStatus{State: "idle", SessionFile: first, SessionID: "first"})
	a := &k2PausedPi{piAgent: piAgent{label: "Pi"}, read: make(chan struct{}), more: make(chan struct{})}
	s := newSeat(Spec{StatusFile: status, Dir: dir}, a, &fakeHost{alive: true}, "k2-a4-"+filepath.Base(dir), "first")
	s.state, s.poll = "idle", time.Millisecond
	go s.watch()
	t.Cleanup(s.Close)
	select {
	case <-a.read:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher never looked")
	}
	// Between two looks: a turn settles on the old session, and pi forks.
	at := time.Now().Add(time.Millisecond)
	turn := piTurnLines("t1", "label the jars", "Labelled.", at)
	appendLines(t, first, turn...)
	writeLines(t, forked, append(append([]string{piHeaderLine("forked", at.Add(10*time.Millisecond))}, piTurnLines("t0", "sort the shelves", "Sorted.", old)...), turn...)...)
	writePiStatus(t, status, piStatus{State: "idle", SessionFile: forked, SessionID: "forked"})
	a.look(t) // reads the turn from the old session, sees the move
	a.look(t) // opens the fork
	a.look(t)
	appendLines(t, forked, piTurnLines("t2", "dust them", "Dusted.", time.Now().Add(time.Millisecond))...)
	a.look(t)
	var turns []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindTurn {
			turns = append(turns, ev.Text)
		}
	}
	if n := slices.Index(turns, "Labelled."); n < 0 || slices.Contains(turns[n+1:], "Labelled.") || !slices.Contains(turns, "Dusted.") {
		t.Fatalf("turns %q; want Labelled. once, then Dusted.", turns)
	}
}

// A7: pi moves to a session whose file it has written already: the seat
// records the move once, with the session and its log, so no record ends
// between a move without its log and the log. A move to a session pi has
// not written yet is recorded at once, without the log, and the log
// follows once the file appears.
func TestK2APiMoveToAWrittenSessionIsRecordedOnce(t *testing.T) {
	isolatePi(t)
	for _, written := range []bool{true, false} {
		w := newPiSwitchSeat(t, time.Time{}, []string{piHeaderLine("first", time.Now())}, entries(0))
		w.until(5*time.Second, innerIs(w.first))
		second := filepath.Join(w.dir, "second.jsonl")
		if written {
			writeLines(t, second, piHeaderLine("second", time.Now()))
		}
		writePiStatus(t, w.status, piStatus{State: "idle", SessionFile: second, SessionID: "second", SessionEntries: entries(0)})
		var moves []string
		record := func(evs []Event) {
			for _, ev := range evs {
				if ev.Kind == KindInner {
					moves = append(moves, ev.InnerID+" "+filepath.Base(ev.Transcript))
				}
			}
		}
		if !written {
			record(w.until(5*time.Second, func(ev Event) bool { return ev.Kind == KindInner }))
			writeLines(t, second, piHeaderLine("second", time.Now()))
		}
		record(w.until(5*time.Second, innerIs(second)))
		record(w.quiet(looks(4)))
		want := []string{"second second.jsonl"}
		if !written {
			want = []string{"second .", "second second.jsonl"}
		}
		if !slices.Equal(moves, want) {
			t.Fatalf("written %v: recorded %q; want %q", written, moves, want)
		}
	}
}
