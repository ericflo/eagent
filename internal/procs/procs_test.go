package procs

import (
	"context"
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
