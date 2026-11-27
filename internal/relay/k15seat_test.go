package relay

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// fakeCodex puts a codex command of this script on PATH.
func fakeCodex(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// workingCodex is a working Codex seat whose session id is known.
func workingCodex(t *testing.T) (*Seat, *fakeHost) {
	t.Helper()
	saved := typeSettle
	typeSettle = time.Millisecond
	t.Cleanup(func() { typeSettle = saved })
	h := &fakeHost{alive: true, screen: "• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything"}
	s := newSeat(Spec{Name: "codex"}, codexAgent{}, h, "k15", "019c0000-0000-7000-8000-000000000001")
	s.state = "working"
	return s, h
}

// A codex queue command that fails, by an exit code or by a signal of its
// own, is followed by typing the message, as on main.
func TestK15ACodexQueueThatFailsIsTyped(t *testing.T) {
	for _, script := range []string{"echo no such thread >&2; exit 2", "kill -SEGV $$"} {
		fakeCodex(t, script)
		s, h := workingCodex(t)
		if _, err := s.Send("r1", "then run the tests", "after_turn"); err != nil {
			t.Fatalf("%s: %v", script, err)
		}
		h.mu.Lock()
		typed := len(h.typed)
		h.mu.Unlock()
		if typed != 1 {
			t.Fatalf("%s: typed %d times", script, typed)
		}
	}
}

// A codex queue command that is slow and then answers with a failure (it
// queued nothing). While it ran, Codex's turn ended and no other started.
// The send is not taken for one Codex dropped from its queue while its
// hand-over has not answered: nothing is reported, and when the command
// fails the message is typed, as after any failed hand-over. (Reported
// unconfirmed meanwhile, the failed hand-over was answered "held for
// Codex" and never typed.)
func TestK15ASlowCodexQueueThatFailsIsStillTyped(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "go")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	fakeCodex(t, "read x < "+fifo+"; echo thread busy >&2; exit 2")
	s, h := workingCodex(t)
	type result struct {
		msg string
		err error
	}
	done := make(chan result, 1)
	go func() {
		msg, err := s.Send("r1", "then run the tests", "after_turn")
		done <- result{msg, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(s.Snapshot().Pending) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("never tracked")
		}
		runtime.Gosched()
	}
	// Codex's turn ends while the command runs, and none follows.
	h.mu.Lock()
	h.screen = "› Ask Codex to do anything\n\n  100% context left"
	h.mu.Unlock()
	now := time.Now()
	s.mu.Lock()
	s.state, s.stateAt, s.turnEndAt = "idle", now.Add(-time.Minute), now
	s.mu.Unlock()
	graced := now.Add(heldGrace + time.Second)
	s.leftOvers(graced, true)
	later := graced.Add(46 * time.Second)
	s.checkScreen(later)
	s.checkScreen(later)
	if got := deliveries(drain(s.events)); len(got) != 0 {
		t.Fatalf("reported %+v while the hand-over had not answered", got)
	}
	// Now the command answers: it failed, nothing was queued.
	f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("x\n")
	f.Close()
	res := <-done
	h.mu.Lock()
	typed := len(h.typed)
	h.mu.Unlock()
	if res.err != nil || typed != 1 {
		t.Fatalf("codex queue failed with an answer; send said %q, %v and typed %d times", res.msg, res.err, typed)
	}
}

// A hand-over that answered: from then on the send is one the turn's end
// can leave over, as before.
func TestK15AQueuedSendTheTurnsEndLeftIsStillReported(t *testing.T) {
	fakeCodex(t, "exit 0")
	s, h := workingCodex(t)
	if msg, err := s.Send("r1", "then run the tests", "after_turn"); err != nil {
		t.Fatalf("%q %v", msg, err)
	}
	h.mu.Lock()
	h.screen = "› Ask Codex to do anything\n\n  100% context left"
	h.mu.Unlock()
	now := time.Now()
	s.mu.Lock()
	s.state, s.stateAt, s.turnEndAt = "idle", now.Add(-time.Minute), now
	s.mu.Unlock()
	graced := now.Add(heldGrace + time.Second)
	s.leftOvers(graced, true)
	later := graced.Add(46 * time.Second)
	s.checkScreen(later)
	s.checkScreen(later)
	got := deliveries(drain(s.events))
	if len(got) != 1 || got[0].SendID != "r1" || got[0].Outcome != OutcomeUnconfirmed {
		t.Fatalf("reported %+v; want r1 unconfirmed", got)
	}
}
