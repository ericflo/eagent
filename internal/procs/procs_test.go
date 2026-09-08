package procs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartAndOutput(t *testing.T) {
	m := NewManager()
	p, err := m.Start(Spec{Command: "echo hello; echo err >&2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Wait(context.Background(), 5*time.Second) {
		t.Fatal("did not exit")
	}
	out, cur := p.Output(0)
	if !strings.Contains(out, "hello") || !strings.Contains(out, "err") {
		t.Fatalf("output = %q", out)
	}
	if p.ExitCode() != 3 || p.Status() != Exited {
		t.Fatalf("exit=%d status=%s", p.ExitCode(), p.Status())
	}
	more, _ := p.Output(cur)
	if more != "" {
		t.Fatalf("expected no new output, got %q", more)
	}
	select {
	case got := <-m.Exited():
		if got.Handle != p.Handle {
			t.Fatal("wrong proc on exited channel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no exit notification")
	}
}

func TestLargeOutputNotTruncatedAt4K(t *testing.T) {
	m := NewManager()
	p, err := m.Start(Spec{Command: "head -c 300000 /dev/zero | tr '\\0' 'x'"})
	if err != nil {
		t.Fatal(err)
	}
	p.Wait(context.Background(), 5*time.Second)
	time.Sleep(100 * time.Millisecond)
	out, _ := p.Output(0)
	if len(out) != 300000 {
		t.Fatalf("got %d bytes, want 300000", len(out))
	}
}

func TestTimeoutKillsProcessGroup(t *testing.T) {
	m := NewManager()
	p, err := m.Start(Spec{Command: "sleep 30 & sleep 30", Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Wait(context.Background(), 5*time.Second) {
		t.Fatal("timeout did not kill the process")
	}
	if p.Status() != Timeout {
		t.Fatalf("status = %s", p.Status())
	}
}

func TestStdinWrite(t *testing.T) {
	m := NewManager()
	p, err := m.Start(Spec{Command: "read x; echo got:$x"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Wait(context.Background(), 200*time.Millisecond) {
		t.Fatal("should be waiting on stdin")
	}
	if err := p.Write("abc\n", true); err != nil {
		t.Fatal(err)
	}
	p.Wait(context.Background(), 5*time.Second)
	time.Sleep(50 * time.Millisecond)
	out, _ := p.Output(0)
	if !strings.Contains(out, "got:abc") {
		t.Fatalf("output = %q", out)
	}
}

func TestExtendAndKill(t *testing.T) {
	m := NewManager()
	p, err := m.Start(Spec{Command: "sleep 60", Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	p.Extend(10 * time.Second)
	if p.Wait(context.Background(), 500*time.Millisecond) {
		t.Fatal("extend did not take effect")
	}
	p.Kill()
	if !p.Wait(context.Background(), 5*time.Second) || p.Status() != Killed {
		t.Fatalf("kill failed: %s", p.Status())
	}
}

func TestBufferCapKeepsHeadAndTail(t *testing.T) {
	m := NewManager()
	// 6MB of numbered lines.
	p, err := m.Start(Spec{Command: "seq 1 600000 | awk '{printf \"%09d\\n\", $1}'"})
	if err != nil {
		t.Fatal(err)
	}
	p.Wait(context.Background(), 20*time.Second)
	time.Sleep(200 * time.Millisecond)
	out, _ := p.Output(0)
	if !strings.HasPrefix(out, "000000001\n") {
		t.Fatalf("head lost: %q", out[:20])
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "000600000") {
		t.Fatalf("tail lost: %q", out[len(out)-20:])
	}
	if !strings.Contains(out, "bytes of output dropped") {
		t.Fatal("no drop marker")
	}
	if len(out) > MaxBuffer+200 {
		t.Fatalf("buffer not capped: %d", len(out))
	}
}

func TestOutputCursorAcrossDrop(t *testing.T) {
	m := NewManager()
	p, err := m.Start(Spec{Command: "seq 1 600000 | awk '{printf \"%09d\\n\", $1}'"})
	if err != nil {
		t.Fatal(err)
	}
	p.Wait(context.Background(), 20*time.Second)
	time.Sleep(200 * time.Millisecond)
	first, cur := p.Output(0)
	if cur != 6000000 {
		t.Fatalf("logical total = %d", cur)
	}
	// A cursor taken before the drop that now falls in the hole yields the
	// marker and the tail; a cursor in the tail yields exactly the remainder.
	inHole, _ := p.Output(MaxBuffer/4 + 10)
	if !strings.HasPrefix(inHole, "\n[...") {
		t.Fatalf("hole read should start at the marker: %q", inHole[:30])
	}
	tail, next := p.Output(cur - 20)
	if tail != "000599999\n000600000\n" || next != cur {
		t.Fatalf("tail read = %q next=%d", tail, next)
	}
	if more, _ := p.Output(cur); more != "" {
		t.Fatal("reading at the end should be empty")
	}
	_ = first
}

func TestWaitAfterKillReturnsOnlyWhenReaped(t *testing.T) {
	m := NewManager()
	// Ignores TERM so the KILL escalation (3s) is what ends it.
	p, err := m.Start(Spec{Command: "trap '' TERM; sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	p.Kill()
	if !p.Wait(context.Background(), 10*time.Second) {
		t.Fatal("wait timed out")
	}
	if time.Since(start) < 2*time.Second {
		t.Fatalf("Wait returned after %s, before the process was actually reaped", time.Since(start))
	}
	if p.Status() != Killed || p.ExitCode() != 137 {
		t.Fatalf("status=%s exit=%d", p.Status(), p.ExitCode())
	}
	d1 := p.Duration()
	time.Sleep(50 * time.Millisecond)
	if p.Duration() != d1 {
		t.Fatal("duration kept ticking after exit")
	}
}

// A `cmd &` whose shell has already exited is still the session's process:
// KillAll must reach it through the process group.
func TestKillAllReapsChildrenOfFinishedShells(t *testing.T) {
	dir := t.TempDir()
	alive := filepath.Join(dir, "alive.txt")
	m := NewManager()
	p, err := m.Start(Spec{Command: fmt.Sprintf("(for i in $(seq 1 300); do echo tick >> %s; sleep 0.1; done) & echo started", alive), Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	<-m.Exited()
	if p.Status() == Running {
		t.Fatal("the shell should have exited")
	}
	size := func() int64 {
		fi, err := os.Stat(alive)
		if err != nil {
			return 0
		}
		return fi.Size()
	}
	before := size()
	time.Sleep(400 * time.Millisecond)
	if size() <= before {
		t.Fatal("the background child should still be writing before KillAll")
	}
	m.KillAll()
	time.Sleep(300 * time.Millisecond)
	a := size()
	time.Sleep(500 * time.Millisecond)
	if b := size(); b != a {
		t.Fatalf("the child survived KillAll: %d -> %d bytes", a, b)
	}
}

// The harness's own credentials are stripped from a command's environment;
// everything else is inherited.
func TestEnvDenyStripsNamedVariables(t *testing.T) {
	t.Setenv("EAGENT_TEST_SECRET", "marker-secret")
	t.Setenv("EAGENT_TEST_KEEP", "marker-keep")
	m := NewManager()
	p, err := m.Start(Spec{Command: "env", Cwd: t.TempDir(), EnvDeny: []string{"EAGENT_TEST_SECRET"}})
	if err != nil {
		t.Fatal(err)
	}
	<-m.Exited()
	out, _ := p.Output(0)
	if strings.Contains(out, "marker-secret") {
		t.Fatal("a denied variable reached the command")
	}
	if !strings.Contains(out, "EAGENT_TEST_KEEP=marker-keep") {
		t.Fatalf("an ordinary variable was lost: %s", out)
	}
}

// A child forked after its shell has exited (the usual shape of `server &`)
// is still the session's, and KillGroup reaches it.
func TestKillGroupReachesChildrenForkedAfterTheShellExited(t *testing.T) {
	m := NewManager()
	p, err := m.Start(Spec{Command: "sh -c 'sleep 0.4; sleep 300; :' >/dev/null 2>&1 & echo started", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	<-m.Exited()
	time.Sleep(900 * time.Millisecond) // the second sleep is forked after the reap
	members := func() int { return len(groupMembers(p.PID(), 1)) }
	if members() == 0 {
		t.Skip("cannot observe process groups here")
	}
	p.KillGroup()
	time.Sleep(200 * time.Millisecond)
	if n := members(); n != 0 {
		t.Fatalf("%d group member(s) survived KillGroup", n)
	}
}
