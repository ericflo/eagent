package relay

import (
	"errors"
	"os"
	"path/filepath"
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

// A codex queue command killed at its bound may have queued the message
// before it hung. Typing the message as well could put it into Codex
// twice: the send fails as one that may have gone, and nothing is typed.
func TestK15ACodexQueueKilledAtItsBoundIsNotTypedAgain(t *testing.T) {
	saved := codexQueueBound
	codexQueueBound = 50 * time.Millisecond
	t.Cleanup(func() { codexQueueBound = saved })
	fakeCodex(t, "exec sleep 5")
	h := &fakeHost{alive: true, screen: "• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything"}
	s := newSeat(Spec{Name: "codex"}, codexAgent{}, h, "k15", "019c0000-0000-7000-8000-000000000001")
	s.state = "working"
	_, err := s.Send("r1", "then run the tests", "after_turn")
	if err == nil || !errors.As(err, new(*TypingError)) {
		t.Fatalf("send: %v; want the failure of a hand-over that may have gone", err)
	}
	h.mu.Lock()
	typed := len(h.typed)
	h.mu.Unlock()
	if typed != 0 || len(s.Snapshot().Pending) != 0 {
		t.Fatalf("typed %d, still pending %v", typed, s.Snapshot().Pending)
	}
}

// A codex queue command that answers with a failure queued nothing: the
// message is typed, as before.
func TestK15ACodexQueueThatFailsIsTyped(t *testing.T) {
	saved := typeSettle
	typeSettle = time.Millisecond
	t.Cleanup(func() { typeSettle = saved })
	fakeCodex(t, "echo no such thread >&2; exit 2")
	h := &fakeHost{alive: true, screen: "• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything"}
	s := newSeat(Spec{Name: "codex"}, codexAgent{}, h, "k15", "019c0000-0000-7000-8000-000000000001")
	s.state = "working"
	if _, err := s.Send("r1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	typed := len(h.typed)
	h.mu.Unlock()
	if typed != 1 {
		t.Fatalf("typed %d times", typed)
	}
}
