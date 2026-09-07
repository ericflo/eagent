// Package procs runs shell commands asynchronously.
//
// Every command starts in its own process group under a handle and keeps
// running while the actor that started it goes on to do other things. The
// manager owns the lifecycle: bounded output capture, stdin, deadlines,
// extension, and cancellation of the whole group.
package procs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Spec describes a command to start.
type Spec struct {
	Handle  string
	Command string
	Cwd     string
	Env     []string
	Timeout time.Duration // zero means no deadline
	Owner   string        // actor or task id that started it; informational
}

// Status is the lifecycle state of a process.
type Status string

const (
	Running Status = "running"
	Exited  Status = "exited"
	Killed  Status = "killed"  // cancelled by the harness or an actor
	Timeout Status = "timeout" // deadline expired
	Lost    Status = "lost"    // was running when the runner died
)

// MaxBuffer caps the bytes retained per process. When exceeded the middle of
// the output is dropped and a marker inserted, so the head (usually the
// command echo / first errors) and the tail (usually the result) survive.
const MaxBuffer = 4 << 20

// Proc is one running or finished command.
type Proc struct {
	Spec
	Started time.Time

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	buf      []byte // head + marker + tail once output has been dropped
	written  int    // total bytes the process has produced (logical length)
	dropped  int    // bytes removed from the middle
	headLen  int    // bytes of buf that are the preserved head (0 = nothing dropped)
	markLen  int    // bytes of buf that are the drop marker
	status   Status
	exitCode int
	ended    time.Time
	deadline time.Time
	timer    *time.Timer
	done     chan struct{} // output pipe drained
	reaped   chan struct{} // cmd.Wait returned
	waiters  []chan struct{}
}

// Manager tracks processes for one session.
type Manager struct {
	mu     sync.Mutex
	procs  map[string]*Proc
	order  []string
	exited chan *Proc
	seq    int
}

// NewManager returns an empty manager. Exited returns a channel that receives
// every process as it finishes.
func NewManager() *Manager {
	return &Manager{procs: map[string]*Proc{}, exited: make(chan *Proc, 256)}
}

// Exited delivers processes as they finish, in completion order.
func (m *Manager) Exited() <-chan *Proc { return m.exited }

// NextHandle allocates a short unique handle.
func (m *Manager) NextHandle() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	return fmt.Sprintf("p%d", m.seq)
}

// SetSeq makes handles continue after a replayed session.
func (m *Manager) SetSeq(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n > m.seq {
		m.seq = n
	}
}

// Start launches spec under bash in a new process group.
func (m *Manager) Start(spec Spec) (*Proc, error) {
	if spec.Handle == "" {
		spec.Handle = m.NextHandle()
	}
	cmd := exec.Command("bash", "-c", spec.Command)
	cmd.Dir = spec.Cwd
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p := &Proc{Spec: spec, cmd: cmd, status: Running, done: make(chan struct{}), reaped: make(chan struct{})}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	p.stdin = stdin
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}
	pw.Close() // child holds its copy
	p.Started = time.Now()
	if spec.Timeout > 0 {
		p.deadline = p.Started.Add(spec.Timeout)
		p.timer = time.AfterFunc(spec.Timeout, func() { p.kill(Timeout) })
	}
	m.mu.Lock()
	m.procs[spec.Handle] = p
	m.order = append(m.order, spec.Handle)
	m.mu.Unlock()

	go p.pump(pr)
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if p.timer != nil {
			p.timer.Stop()
		}
		p.ended = time.Now()
		if p.status == Running {
			p.status = Exited
		}
		p.exitCode = exitCode(err)
		waiters := p.waiters
		p.waiters = nil
		close(p.reaped)
		p.mu.Unlock()
		// pump closes done once the pipe drains; give it a moment, but never
		// block the exit notification on a grandchild holding the pipe open.
		select {
		case <-p.done:
		case <-time.After(500 * time.Millisecond):
		}
		for _, w := range waiters {
			close(w)
		}
		m.exited <- p
	}()
	return p, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if st, ok := ee.Sys().(syscall.WaitStatus); ok && st.Signaled() {
			return 128 + int(st.Signal())
		}
		return ee.ExitCode()
	}
	return -1
}

func (p *Proc) pump(r io.ReadCloser) {
	defer close(p.done)
	defer r.Close()
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			p.mu.Lock()
			p.buf = append(p.buf, chunk[:n]...)
			p.written += n
			if len(p.buf) > MaxBuffer {
				p.drop()
			}
			p.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// drop discards the middle of the buffer, keeping the first quarter of the
// cap as the head and the last half as the tail, with a marker between.
// Caller holds p.mu.
func (p *Proc) drop() {
	head := MaxBuffer / 4
	tail := MaxBuffer / 2
	var headBytes []byte
	if p.headLen == 0 {
		headBytes = p.buf[:head]
	} else {
		headBytes = p.buf[:p.headLen]
		head = p.headLen
	}
	tailBytes := p.buf[len(p.buf)-tail:]
	// Everything between the head and the new tail is gone (minus any old marker).
	p.dropped = p.written - head - tail
	marker := []byte(fmt.Sprintf("\n[... %d bytes of output dropped ...]\n", p.dropped))
	nb := make([]byte, 0, head+len(marker)+tail)
	nb = append(nb, headBytes...)
	nb = append(nb, marker...)
	nb = append(nb, tailBytes...)
	p.buf = nb
	p.headLen = head
	p.markLen = len(marker)
}

// Get looks up a process by handle.
func (m *Manager) Get(handle string) (*Proc, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[handle]
	return p, ok
}

// All returns processes in start order.
func (m *Manager) All() []*Proc {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Proc, 0, len(m.order))
	for _, h := range m.order {
		out = append(out, m.procs[h])
	}
	return out
}

// Running returns processes that have not finished.
func (m *Manager) Running() []*Proc {
	var out []*Proc
	for _, p := range m.All() {
		if p.Status() == Running {
			out = append(out, p)
		}
	}
	return out
}

// KillAll terminates every running process and any stragglers left in the
// process groups of finished ones (a `cmd &` that outlived its shell).
func (m *Manager) KillAll() {
	for _, p := range m.All() {
		if p.Status() == Running {
			p.Kill()
		} else if pid := p.PID(); pid > 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL) // ESRCH is fine
		}
	}
}

// Status returns the lifecycle state.
func (p *Proc) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// ExitCode is valid once the process is no longer Running.
func (p *Proc) ExitCode() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitCode
}

// Duration is the wall time from start to exit (or now).
func (p *Proc) Duration() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended.IsZero() {
		return time.Since(p.Started)
	}
	return p.ended.Sub(p.Started)
}

// Deadline returns when the process will be killed, or zero.
func (p *Proc) Deadline() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deadline
}

// PID returns the process id.
func (p *Proc) PID() int {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// Wait blocks until the process exits or the context ends. It returns true
// if the process finished.
func (p *Proc) Wait(ctx context.Context, d time.Duration) bool {
	p.mu.Lock()
	if !p.ended.IsZero() {
		p.mu.Unlock()
		return true
	}
	ch := make(chan struct{})
	p.waiters = append(p.waiters, ch)
	p.mu.Unlock()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// Output returns the retained output from logical byte offset cursor, and
// the new cursor. Offsets count every byte the process ever wrote, so a
// cursor stays valid after the middle of the buffer has been dropped; a
// cursor that falls in the dropped region yields the marker and the tail.
func (p *Proc) Output(cursor int) (string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	total := p.written
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		return "", total
	}
	if p.headLen == 0 {
		return string(p.buf[cursor:]), total
	}
	tailLen := len(p.buf) - p.headLen - p.markLen
	tailStart := total - tailLen
	switch {
	case cursor < p.headLen:
		return string(p.buf[cursor:]), total
	case cursor >= tailStart:
		return string(p.buf[len(p.buf)-(total-cursor):]), total
	default:
		return string(p.buf[p.headLen:]), total
	}
}

// Tail returns up to n bytes from the end of the output.
func (p *Proc) Tail(n int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.buf) <= n {
		return string(p.buf)
	}
	return string(p.buf[len(p.buf)-n:])
}

// Write sends bytes to the process's stdin; closeAfter closes stdin.
func (p *Proc) Write(s string, closeAfter bool) error {
	p.mu.Lock()
	stdin := p.stdin
	status := p.status
	p.mu.Unlock()
	if status != Running {
		return fmt.Errorf("process is %s", status)
	}
	if stdin == nil {
		return errors.New("stdin already closed")
	}
	if s != "" {
		if _, err := io.WriteString(stdin, s); err != nil {
			return err
		}
	}
	if closeAfter {
		p.mu.Lock()
		p.stdin = nil
		p.mu.Unlock()
		return stdin.Close()
	}
	return nil
}

// Extend moves the deadline to now+d.
func (p *Proc) Extend(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status != Running {
		return
	}
	if p.timer != nil && !p.timer.Stop() {
		return // the deadline already fired; the kill is under way
	}
	if d <= 0 {
		p.deadline = time.Time{}
		p.timer = nil
		return
	}
	p.deadline = time.Now().Add(d)
	p.timer = time.AfterFunc(d, func() { p.kill(Timeout) })
}

// Kill terminates the process group: TERM, then KILL after a grace period.
func (p *Proc) Kill() { p.kill(Killed) }

func (p *Proc) kill(reason Status) {
	p.mu.Lock()
	if p.status != Running {
		p.mu.Unlock()
		return
	}
	p.status = reason
	pid := 0
	if p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	p.mu.Unlock()
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	go func() {
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}()
}

// Describe renders a one-line status for listings.
func (p *Proc) Describe() string {
	cmd := strings.ReplaceAll(p.Command, "\n", " ")
	if len(cmd) > 80 {
		cmd = cmd[:77] + "..."
	}
	st := p.Status()
	if st == Running {
		return fmt.Sprintf("%s  running %s  %s", p.Handle, p.Duration().Round(time.Second), cmd)
	}
	return fmt.Sprintf("%s  %s exit=%d %s  %s", p.Handle, st, p.ExitCode(), p.Duration().Round(time.Millisecond), cmd)
}
