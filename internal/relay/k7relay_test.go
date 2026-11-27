package relay

import (
	"path/filepath"
	"testing"
	"time"
)

// SR4-2. 9888cae holds a typed command's delivered until "the first look
// begun after the typing" has read pi's status file. That look begins up to
// one poll after Enter, which can be before pi has run /new and written its
// status: here pi runs /new only after that look (the test writes pi's
// status once the delivered is out, as a pi slower than the seat's next
// look does). The seat says the command delivered with no move seen, the
// harness applies a change waiting on it (appendOutcome, applyWaitingRepick)
// and the restart kills this seat before any later look could report the
// move, so pi is started again in the session /new left: SR3-1, still.
func TestSR4TypedCommandIsDeliveredBeforePiHasActedOnIt(t *testing.T) {
	t.Skip("known, not fixed: SR4-2, the seat fixer's (Status.Quiet)")
	isolatePi(t)
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	dir := t.TempDir()
	first, second, status := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "second.jsonl"), filepath.Join(dir, "pi-status.json")
	writeLines(t, first, piHeaderLine("first", time.Now()))
	writePiStatus(t, status, piStatus{State: "idle", SessionFile: first, SessionID: "first", SessionEntries: entries(0)})
	entered := make(chan struct{}, 1)
	h := &k1Host{fakeHost: &fakeHost{alive: true}}
	h.enter = func() error { // Enter goes; pi has not run /new yet
		entered <- struct{}{}
		return nil
	}
	s := newSeat(Spec{StatusFile: status, Dir: dir}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "sr4-cmd-"+filepath.Base(dir), "first")
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	deadline := time.After(5 * time.Second)
	next := func() Event {
		select {
		case ev := <-s.events:
			return ev
		case <-deadline:
			t.Fatal("timed out")
			return Event{}
		}
	}
	for ev := next(); !(ev.Kind == KindInner && ev.Transcript == first); ev = next() {
	}
	if _, err := s.Send("c1", "/new", "queue"); err != nil {
		t.Fatal(err)
	}
	<-entered
	var before []string
	for {
		ev := next()
		if ev.Kind == KindInner {
			before = append(before, "inner "+ev.InnerID)
		}
		if ev.Kind == KindDelivered && ev.SendID == "c1" {
			break
		}
	}
	st, _ := readPiStatus(status)
	// pi runs /new now, after the seat's look.
	writeLines(t, second, piHeaderLine("second", time.Now()))
	writePiStatus(t, status, piStatus{State: "idle", SessionFile: second, SessionID: "second", SessionEntries: entries(0)})
	if len(before) == 0 {
		t.Errorf("the seat said /new delivered (acted on) while pi was still on session %q and no move was seen; a change waiting on it restarts pi into that session, and the restart's kill ends this seat before it can report the move", st.SessionID)
	}
}
