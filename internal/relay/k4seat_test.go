package relay

// The sixteenth review's third pass over the seat (sr3seat).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// sr3MoveHost runs onAlive inside the watcher's liveness check, which a
// look makes after it has read the transcript and before it polls pi's
// status file: the moment pi can write the end of a run and move to
// another session (an extension's ctx.newSession at the run's end, /new
// typed while pi works) between the two.
type sr3MoveHost struct {
	fakeHost
	once    sync.Once
	onAlive func() bool // true once it has run
	ran     bool
}

func (h *sr3MoveHost) Alive(target string) bool {
	if !h.ran && h.onAlive != nil && h.onAlive() {
		h.ran = true
	}
	return h.fakeHost.Alive(target)
}

// Known limits, thirteenth review, "noticed by its fixers and not
// changed": a reattached Claude or Codex seat with an unknown dialog that
// came up while eagent was down can be typed into. Still so: the history
// ends inside a turn, the transcript has been quiet for a minute and a
// half (under the ten minutes caughtUp waits for), and the screen shows
// neither Claude's prompt nor its busy line but a question of its own.
// Send types the message into it, and its Enter answers it.
func TestSr3SeatReattachedClaudeTypesIntoADialogThatCameUpWhileEagentWasDown(t *testing.T) {
	savedWait := readyWait
	readyWait = 100 * time.Millisecond // the wait for the agent's own screen, shortened
	t.Cleanup(func() { readyWait = savedWait })
	dir := t.TempDir()
	transcript := filepath.Join(dir, "sess.jsonl")
	at := time.Now().Add(-2 * time.Minute)
	writeLines(t, transcript,
		claudeUser(at, "migrate the database"),
		fmt.Sprintf(`{"type":"assistant","timestamp":"%s","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"make migrate","description":"Run the migration"}}]}}`, stamp(at.Add(time.Second))),
	)
	quiet := time.Now().Add(-90 * time.Second)
	if err := os.Chtimes(transcript, quiet, quiet); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(transcript)
	h := &fakeHost{alive: true, screen: "make migrate\nThis will drop table users. Continue?\n❯ 1. Yes\n  2. No\n"}
	s := attachSeat(Spec{Dir: dir, State: "working", Read: st.Size()}, claudeAgent{}, h, "sr3-claude-dialog", "id", transcript, quiet)
	s.poll = piLook
	done := make(chan struct{})
	go s.watch()
	go func() {
		defer close(done)
		for range s.Events() {
		}
	}()
	t.Cleanup(func() { s.Close(); <-done })
	if !eventually(func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.catchingUp }) {
		t.Fatal("never caught up")
	}
	saved := typeSettle
	typeSettle = time.Millisecond
	t.Cleanup(func() { typeSettle = saved })
	res, err := s.Send("s1", "also update the README", "queue")
	h.mu.Lock()
	typed, keys := append([]string(nil), h.typed...), append([]string(nil), h.keys...)
	h.mu.Unlock()
	if len(typed) > 0 {
		t.Errorf("typed %q into a screen showing the dialog (keys %q; Send said %q, %v); want it refused as for a seat that cannot tell the screen is the agent's own", typed, keys, res, err)
	}
	// Once the screen shows Claude's own prompt, a send goes.
	h.mu.Lock()
	h.screen = "> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	h.mu.Unlock()
	if _, err := s.Send("s2", "also update the README", "queue"); err != nil || len(k1Typed(h)) != 1 {
		t.Errorf("at Claude's prompt: Send %v, typed %q", err, k1Typed(h))
	}
}

// A restored send is confirmed by a line before the record's read mark
// (history) when the line is stamped no earlier than the send was
// recorded. That line was read before eagent stopped, and the record has
// its outcome: it confirmed the earlier send of the same words. Two
// replies of the same words a moment apart ("yes", "yes"): the second is
// recorded while the first is being typed, so the first's arrival is
// stamped after the second's record. The second's Enter does not go (its
// words are left in the input box) and eagent restarts before the seat
// presses it again. The reattach confirms the second from the first's
// arrival: the record says it arrived, and nothing presses or reports it.
func TestSr3SeatHistoryLineAlreadyReadConfirmsALaterRestoredSendOfTheSameWords(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "sess.jsonl")
	r1 := time.Now().Add(-30 * time.Second) // the first "yes" recorded
	r2 := r1.Add(100 * time.Millisecond)    // the second, while the first is typed
	lines := claudeTurnLines("start", "Shall I deploy?", r1.Add(-time.Minute))
	lines = append(lines, claudeTurnLines("yes", "Deployed.", r1.Add(400*time.Millisecond))...)
	writeLines(t, transcript, lines...)
	st, _ := os.Stat(transcript)
	// The record: the first confirmed at its arrival (the mark is the end
	// of what was read, past the turn it started), the second waiting.
	w := reattach(t, claudeAgent{}, Spec{Dir: dir, State: "idle", Read: st.Size()}, transcript, r1.Add(2*time.Second),
		Restored{ID: "r2", Text: "yes", Mode: "queue", At: r2})
	w.caughtUp(t)
	w.settle()
	if ok, _ := w.delivered(); len(ok) > 0 {
		t.Errorf("confirmed %v from a line the record had read and used for the first \"yes\"; want r2 left to be pressed or reported", ok)
	}
}

// e8fd70f reads a session to its end before following pi to another, but
// "its end" is where the read stopped, not where pi stopped writing. pi
// writes the end of a run in the session it leaves (a send's arrival, the
// reply, the settled mark) and moves on; when that lands between the
// watcher's read and its look at the status file, the next look drops the
// session left without reading it again. The arrival is never confirmed
// (the send is later reported unconfirmed, "never showed up", though it
// arrived), the turn's report is never given, and the seat says pi is
// working while it waits idle in the new session.
func TestSr3SeatPiMoveRightAfterAReadLosesTheEndOfTheSessionLeft(t *testing.T) {
	isolatePi(t)
	saved := aliveEvery
	aliveEvery = time.Nanosecond // a liveness check at every look
	t.Cleanup(func() { aliveEvery = saved })
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "second.jsonl")
	status := filepath.Join(dir, "pi-status.json")
	at := time.Now().Add(-time.Minute)
	ts := func(d time.Duration) string { return stamp(at.Add(d)) }
	writeLines(t, first,
		piHeaderLine("first", at),
		fmt.Sprintf(`{"type":"message","id":"u0","parentId":null,"timestamp":"%s","message":{"role":"user","content":[{"type":"text","text":"run the tests"}]}}`, ts(time.Millisecond)),
		fmt.Sprintf(`{"type":"message","id":"a0","parentId":"u0","timestamp":"%s","message":{"role":"assistant","content":[{"type":"toolCall","name":"bash","arguments":{"command":"go test ./..."}}],"stopReason":"toolUse"}}`, ts(2*time.Millisecond)),
	)
	writePiStatus(t, status, piStatus{State: "working", SessionFile: first, SessionID: "first", SessionEntries: entries(0)})
	h := &sr3MoveHost{fakeHost: fakeHost{alive: true}}
	s := newSeat(Spec{StatusFile: status, Dir: dir}, piAgent{label: "Pi"}, h, "sr3-pi-move", "first")
	s.state = "idle"
	s.poll = piLook
	h.onAlive = func() bool {
		s.mu.Lock()
		ready := s.state == "working" && s.transcript == first
		s.mu.Unlock()
		if !ready {
			return false
		}
		// A message typed while pi worked, watched from now (as Send's
		// trackSend), arrives in the run; the run ends; pi moves on.
		s.trackSend(&pending{id: "q1", text: "also check the logs", mode: "queue"})
		appendLines(t, first, piTurnLines("t1", "also check the logs", "All green, logs clean.", at.Add(time.Second))...)
		writeLines(t, second, piHeaderLine("second", at.Add(2*time.Second)))
		writePiStatus(t, status, piStatus{State: "idle", SessionFile: second, SessionID: "second", SessionEntries: entries(0)})
		return true
	}
	var mu sync.Mutex
	var got []Event
	done := make(chan struct{})
	go s.watch()
	go func() {
		defer close(done)
		for ev := range s.Events() {
			if ev.Kind == KindActivity {
				continue
			}
			mu.Lock()
			got = append(got, ev)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() { s.Close(); <-done })
	// Wait until the seat follows pi to the second session.
	if !eventually(func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, ev := range got {
			if ev.Kind == KindInner && ev.Transcript == second {
				return true
			}
		}
		return false
	}) {
		t.Fatal("the seat never followed pi to the second session")
	}
	within(looks(quietLooks), func() bool { return false })
	mu.Lock()
	defer mu.Unlock()
	var confirmed, turn bool
	for _, ev := range got {
		switch {
		case ev.Kind == KindDelivered && ev.SendID == "q1" && ev.OK:
			confirmed = true
		case ev.Kind == KindTurn && ev.Text == "All green, logs clean.":
			turn = true
		}
	}
	s.mu.Lock()
	state, waiting := s.state, len(s.pending)
	s.mu.Unlock()
	if !confirmed || !turn || state != "idle" {
		t.Errorf("send confirmed %v (still watched: %d), turn reported %v, seat state %q; want the arrival and the turn pi wrote in the session it left, and idle", confirmed, waiting, turn, state)
	}
}

// The reader's suspicion: the agent's own command (/compact) is confirmed
// once typed, and that event can be lost at a quit (the watcher has
// closed the seat's events). The next run restores it as a send the
// record still waits on; a command never shows arriving in a transcript,
// so after the wait it is reported unconfirmed. Not with a send's words
// ("does not show it arriving … send it again"): it may have run, and the
// words would have it run twice.
func TestK4ARestoredAgentCommandIsNotCalledLostForNotShowing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	since := time.Now().Add(-time.Minute)
	writeLines(t, path, claudeTurnLines("sort the shelves", "Sorted.", since.Add(-time.Hour))...)
	w := reattach(t, claudeAgent{}, Spec{Dir: dir, State: "idle"}, path, since, Restored{ID: "r1", Text: "/compact", Mode: "queue", At: since.Add(-time.Second)})
	w.caughtUp(t)
	w.h.mu.Lock()
	w.h.screen = "> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	w.h.mu.Unlock()
	start := time.Now()
	w.s.mu.Lock()
	w.s.stateAt = start.Add(-time.Minute)
	w.s.mu.Unlock()
	if !w.reportedAt(start.Add(time.Hour)) { // nothing is decided on one look
		t.Fatal("never reported")
	}
	_, failed := w.delivered()
	if strings.Contains(failed[0], "does not show it arriving") || strings.Contains(failed[0], "send it again") {
		t.Errorf("the restored command is reported as a message that never arrived: %q", failed[0])
	}
}

// The reader's suspicion: fold leaves a turn's start or end for the end
// of a dialog pi reports (heldByDialog), and the history a reattached seat
// re-reads did not. pi's dialog came up mid-turn (the history so far says
// working); the rest of the history shows the turn ending. When the dialog
// closes, the seat goes back to what the history last said (idle), not to
// what it said when the dialog opened (working).
func TestK4AHistoryUnderAPiDialogSetsWhatTheSeatGoesBackTo(t *testing.T) {
	since := time.Now()
	s := attachSeat(Spec{}, piAgent{name: "pi", label: "Pi", binary: "pi"}, &fakeHost{alive: true}, "k4-hist-dialog", "id", "", since)
	s.fold(obs{kind: "working", at: since.Add(-time.Minute)})
	s.reportDialog("Allow rm -rf build?")
	s.fold(obs{kind: "turn", text: "Done.", at: since.Add(-time.Second)})
	s.reportDialog("")
	if st := s.Snapshot().State; st != "idle" {
		t.Fatalf("after the dialog the seat is %s; the history showed the turn over", st)
	}
}

// A typed command's delivered event goes out once the watcher's next look
// has read what the agent did with it: pi's /new moves it to a new
// session, and the move (KindInner) comes first, so a model change
// waiting on the command restarts pi into the session /new opened, not
// the one it left. It carries no read position (079592e).
func TestK4ATypedCommandIsDeliveredAfterTheMoveItMade(t *testing.T) {
	isolatePi(t)
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	dir := t.TempDir()
	first, second, status := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "second.jsonl"), filepath.Join(dir, "pi-status.json")
	writeLines(t, first, piHeaderLine("first", time.Now()))
	writePiStatus(t, status, piStatus{State: "idle", SessionFile: first, SessionID: "first", SessionEntries: entries(0)})
	h := &k1Host{fakeHost: &fakeHost{alive: true}}
	h.enter = func() error { // pi runs /new: a new session, and its status says so
		writeLines(t, second, piHeaderLine("second", time.Now()))
		writePiStatus(t, status, piStatus{State: "idle", SessionFile: second, SessionID: "second", SessionEntries: entries(0)})
		return nil
	}
	s := newSeat(Spec{StatusFile: status, Dir: dir}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "k4-cmd-"+filepath.Base(dir), "first")
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	var order []string
	deadline := time.After(5 * time.Second)
	next := func() Event {
		select {
		case ev := <-s.events:
			return ev
		case <-deadline:
			t.Fatalf("timed out; saw %q", order)
			return Event{}
		}
	}
	for ev := next(); !(ev.Kind == KindInner && ev.Transcript == first); ev = next() {
	}
	if _, err := s.Send("c1", "/new", "queue"); err != nil {
		t.Fatal(err)
	}
	for {
		ev := next()
		switch {
		case ev.Kind == KindInner:
			order = append(order, "inner "+ev.InnerID)
		case ev.Kind == KindDelivered && ev.SendID == "c1":
			order = append(order, "delivered c1")
			if ev.Read != 0 {
				t.Errorf("the command's delivered says the transcript was read to %d", ev.Read)
			}
		default:
			continue
		}
		if order[len(order)-1] == "delivered c1" {
			break
		}
	}
	if len(order) < 2 || order[0] != "inner second" {
		t.Fatalf("events %q; want pi's move to the session /new opened before the command's delivered", order)
	}
}
