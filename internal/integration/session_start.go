package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SessionStart reports a session started on behalf of a remote request.
type SessionStart struct {
	ID string
	// Host says where the session runs: "in_process" (an eagent server or
	// connector that keeps it interactive) or "detached" (a batch process of
	// its own that ends when the work is done).
	Host        string
	Interactive bool
	Message     string
}

// SessionStarter starts a new session in the project with the given first
// message and returns its id once the session exists on disk.
type SessionStarter func(ctx context.Context, project, prompt string) (SessionStart, error)

// AnnounceEnv names a file a freshly started eagent process writes its
// session id to, so the process that spawned it can report the id.
const AnnounceEnv = "EAGENT_ANNOUNCE_SESSION"

var (
	starterMu sync.Mutex
	starter   SessionStarter = spawnDetachedSession
)

// SetSessionStarter makes phone-initiated sessions run through fn (an eagent
// server hosts them in-process). It returns a function that restores the
// previous starter.
func SetSessionStarter(fn SessionStarter) func() {
	starterMu.Lock()
	defer starterMu.Unlock()
	previous := starter
	starter = fn
	return func() {
		starterMu.Lock()
		defer starterMu.Unlock()
		starter = previous
	}
}

func startSession(ctx context.Context, project, prompt string) (SessionStart, error) {
	starterMu.Lock()
	fn := starter
	starterMu.Unlock()
	return fn(ctx, project, prompt)
}

// spawnCommand builds the detached eagent process; tests substitute it.
var spawnCommand = func(project, prompt, announce string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "-C", project, "-p", prompt)
	cmd.Env = append(os.Environ(), AnnounceEnv+"="+announce)
	return cmd, nil
}

// spawnDetachedSession is the starter when no long-lived host registered
// one: a terminal session that happens to hold the project's connector. The
// new session gets a process of its own (its own session group, so it
// survives this process ending) and runs as a batch session with the phone
// mirror on; it reports its id through AnnounceEnv.
func spawnDetachedSession(ctx context.Context, project, prompt string) (SessionStart, error) {
	dir := filepath.Join(stateDir(project), "spawned")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SessionStart{}, err
	}
	id := randomID()
	announce := filepath.Join(dir, id+".session")
	logPath := filepath.Join(dir, id+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return SessionStart{}, err
	}
	cmd, err := spawnCommand(project, prompt, announce)
	if err != nil {
		logFile.Close()
		return SessionStart{}, err
	}
	cmd.Dir = project
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return SessionStart{}, fmt.Errorf("start eagent: %w", err)
	}
	logFile.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(20 * time.Second)
	for {
		if raw, err := os.ReadFile(announce); err == nil && strings.TrimSpace(string(raw)) != "" {
			_ = os.Remove(announce)
			return SessionStart{ID: strings.TrimSpace(string(raw)), Host: "detached", Interactive: false,
				Message: "Started as a batch session in its own process; it runs until the work is done. Continue it later from eagent's web UI or with eagent -c."}, nil
		}
		select {
		case err := <-exited:
			return SessionStart{}, fmt.Errorf("eagent exited before starting a session (%v): %s", err, logTail(logPath))
		case <-ctx.Done():
			return SessionStart{}, ctx.Err()
		case <-deadline:
			return SessionStart{}, errors.New("the new eagent process did not report a session within 20 seconds; see " + logPath)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func logTail(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return "(no output)"
	}
	text := strings.TrimSpace(string(raw))
	if len(text) > 600 {
		text = "…" + text[len(text)-600:]
	}
	return text
}
