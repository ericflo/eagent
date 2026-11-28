package relay

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// A command's own exit status says whether it did its work. One that
// exited with success in the instant its time bound passed is waited for
// with the bound's error (the kill found a process that had ended and was
// not yet collected); `codex queue` took the message all the same, and
// typing it as well would send it twice.
func TestK18ACommandThatExitedWithSuccessAsItsBoundPassed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	time.Sleep(100 * time.Millisecond) // it has exited; nothing has collected it
	cancel()
	time.Sleep(20 * time.Millisecond) // the bound's kill
	err := cmd.Wait()
	t.Logf("the wait gave: %v", err)
	if !exitedWithSuccess(cmd) {
		t.Fatalf("a command that exited with status 0 is not taken as done (the wait gave %v)", err)
	}
}

// A command that failed, one its bound killed and one that never ran did
// not do their work.
func TestK18ACommandThatDidNotExitWithSuccess(t *testing.T) {
	failed := exec.Command("sh", "-c", "exit 3")
	if err := failed.Run(); err == nil || exitedWithSuccess(failed) {
		t.Fatalf("exit 3: %v, taken as done", err)
	}
	killed, cancel := bounded(30*time.Millisecond, "sleep", "5")
	defer cancel()
	if err := killed.Run(); err == nil || exitedWithSuccess(killed) {
		t.Fatalf("killed at its bound: %v, taken as done", err)
	}
	never := exec.Command("/nonexistent/eagent-k18")
	if err := never.Run(); err == nil || exitedWithSuccess(never) {
		t.Fatalf("never started: %v, taken as done", err)
	}
}
