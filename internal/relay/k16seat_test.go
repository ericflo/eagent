package relay

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A codex queue command that queues the message and exits 0, but leaves a
// child process holding its output open (a helper or server it started),
// is a success: the message is in Codex's queue. CombinedOutput gives up on
// the pipe after the wait and returns exec.ErrWaitDelay. Taken for a
// failure, Send typed the message as well, and it went into Codex twice.
func TestK16ACodexQueueThatSucceedsWithAChildHoldingItsOutputIsNotTypedAgain(t *testing.T) {
	saved := pipeWait
	pipeWait = 20 * time.Millisecond
	t.Cleanup(func() { pipeWait = saved })
	fifo := filepath.Join(t.TempDir(), "hold")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			f.WriteString("x\n")
			f.Close()
		}
	})
	// Queued (exit 0); a child it left keeps stdout open.
	fakeCodex(t, "(read x < "+fifo+") & exit 0")
	s, h := workingCodex(t)
	msg, err := s.Send("r1", "then run the tests", "after_turn")
	h.mu.Lock()
	typed := len(h.typed)
	h.mu.Unlock()
	if err != nil || typed != 0 {
		t.Fatalf("codex queue exited 0 (queued); send said %q, %v and typed it %d times as well", msg, err, typed)
	}
}
