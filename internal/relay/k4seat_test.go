package relay

// The sixteenth review's third pass over the seat (sr3seat).

import (
	"fmt"
	"os"
	"path/filepath"
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
