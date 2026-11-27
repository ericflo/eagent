package relay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SR9-1. c249eef takes a busy line that flickers for granted: a change of
// Stuck needs two screen checks in a row. The quiet turn's end seen on the
// screen (6827736) needs one: Claude's footer stays on screen while it
// works, so one capture without the spinner line ends the turn, and
// heldGrace later the message held for that turn's end is taken back from
// the spool and reported not sent, "Claude's turn ended before it took
// this message", while Claude is still in the turn whose Stop hook would
// have handed it over.
func TestSR9SeatOneFlickerEndsAQuietTurnAndTakesBackItsHeldMessage(t *testing.T) {
	saved, savedSettle := heldGrace, typeSettle
	heldGrace, typeSettle = 0, 0
	t.Cleanup(func() { heldGrace, typeSettle = saved, savedSettle })
	spool := filepath.Join(t.TempDir(), "after_turn")
	busy := "✻ Running… (10m 12s · esc to interrupt)\n" + claudePromptScreen
	h := &fakeHost{alive: true, screen: busy}
	s := newSeat(Spec{Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "sr9-flicker-end", "id")
	s.state = "working"
	if res, err := s.Send("h1", "and then the docs", "after_turn"); err != nil || !strings.HasPrefix(res, "held for") {
		t.Fatalf("send: %q %v; want it held for the turn's end", res, err)
	}
	s.mu.Lock()
	s.stateAt, s.lastLine = time.Now().Add(-11*time.Minute), time.Now().Add(-11*time.Minute)
	s.mu.Unlock()
	look := func(screen string) {
		h.mu.Lock()
		h.screen = screen
		h.mu.Unlock()
		s.checkScreen(time.Now())
	}
	look(busy)               // quiet ten minutes, at work: blocked
	look(claudePromptScreen) // one capture without the spinner line
	look(busy)               // at work again
	_ = drain(s.events)
	s.reportLeftOvers(time.Now()) // the poll after heldGrace
	var notSent []Event
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered {
			notSent = append(notSent, ev)
		}
	}
	if st := s.Snapshot().State; st != "blocked" || len(notSent) != 0 || len(spoolNames(spool)) != 1 {
		t.Fatalf("after one capture without the busy line: state %s, outcomes %+v, spool %v; want still blocked at work, h1 left in the spool for the Stop hook", st, notSent, spoolNames(spool))
	}
}

// SR9-3. A message typed into pi while it works waits in pi's queue until
// the run's next step. When the run stops on a dialog pi reports (an
// extension's confirm), the seat is blocked, not hung, so the 45-second
// check reports the send unconfirmed though pi is mid-run and delivers it
// once the dialog is answered; its arrival then confirms nothing. A seat
// blocked for going quiet while busy keeps such a send (Changed on
// purpose); a run held by a dialog does not.
func TestSR9SeatSendQueuedInPiIsReportedUnconfirmedWhileADialogHoldsTheRun(t *testing.T) {
	savedSettle := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = savedSettle })
	status := filepath.Join(t.TempDir(), "pi-status.json")
	writeStatus := func(prompt string) {
		raw := `{"state":"working","prompt":"` + prompt + `","session_file":"","session_id":""}`
		if err := os.WriteFile(status, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeStatus("")
	h := &fakeHost{alive: true, screen: "working...\n0.4%/1.0M (auto)"}
	s := newSeat(Spec{StatusFile: status}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "sr9-pi-dialog", "sess-1")
	s.state = "working"
	if res, err := s.Send("q1", "also run the linter", "queue"); err != nil || !strings.Contains(res, "while it works") {
		t.Fatalf("send: %q %v", res, err)
	}
	writeStatus("Allow the edit?")
	s.reportDialog("Allow the edit?") // the run stops on an extension's confirm
	_ = drain(s.events)
	s.checkScreen(time.Now().Add(61 * time.Second)) // the dialog up a minute
	writeStatus("")
	s.reportDialog("") // answered: the run goes on
	s.fold(obs{kind: "user", text: "also run the linter", at: time.Now()})
	var got []Event
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered {
			got = append(got, ev)
		}
	}
	if len(got) != 1 || !got[0].OK {
		t.Fatalf("outcomes %+v; want q1 delivered by its arrival after the dialog, never unconfirmed while pi held it mid-run", got)
	}
}

// SR9-2. The same flicker at the screen check that blocks a quiet turn:
// that check alone sets Stuck, and the blocked event says it at once,
// though c249eef says a value of Stuck is said only once two checks in a
// row find it. The harness applies a model change that waited for the
// turn's end on that event (heldBack: Stuck holds nothing back), so the
// agent is started again in the middle of its turn.
func TestSR9SeatOneFlickerAtTheBlockSaysStuck(t *testing.T) {
	busy := "• Running go test ./... (12m 03s • esc to interrupt)\n› "
	h := &fakeHost{alive: true, screen: "$ "}
	s := newSeat(Spec{}, codexAgent{}, h, "sr9-flicker-block", "thread-1")
	s.state, s.stateAt = "working", time.Now().Add(-11*time.Minute)
	s.checkScreen(time.Now()) // the busy line missing at this one capture
	evs := drain(s.events)
	h.mu.Lock()
	h.screen = busy
	h.mu.Unlock()
	s.checkScreen(time.Now()) // at work
	if len(evs) != 1 || evs[0].State != "blocked" || evs[0].Stuck || s.Snapshot().Stuck {
		t.Fatalf("one capture without the busy line said %+v; stuck now %v; want blocked, not said stuck before two checks find it", evs, s.Snapshot().Stuck)
	}
}
