package runtimecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
)

func requestFor(t *testing.T, c *Controller, value float64) Request {
	t.Helper()
	view, err := View(c.Project, c.Session, c.Generation, c.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := Grant(c.Project, c.Session)
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation, Edits: []control.Edit{{Op: "set", Key: "/task_concurrency", Value: value}}}
	raw, _ := json.Marshal(p)
	return Request{ID: newID(), UserID: "fixture-user", Proposal: p, Digest: artifact.Digest(raw), Expires: time.Now().Add(2 * time.Second), Grant: grant}
}

func runLoop(t *testing.T, c *Controller) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-tick.C:
				if err := c.Poll(); err != nil {
					done <- err
					return
				}
			}
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func TestRuntimeAcknowledgementDuplicateAndReplacement(t *testing.T) {
	project := t.TempDir()
	var applied atomic.Int32
	c, err := New(project, "session", FromConfig(config.Defaults()), func(_ Request, _ Values, _ map[string]any) error { applied.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	q := requestFor(t, c, 5)
	stop := runLoop(t, c)
	result, err := Deliver(context.Background(), project, "session", q, false)
	if err != nil || result.Status != "succeeded" {
		t.Fatal(result, err)
	}
	duplicate, err := Deliver(context.Background(), project, "session", q, true)
	if err != nil || duplicate.Result["version"] != result.Result["version"] || applied.Load() != 1 {
		t.Fatal("duplicate reapplied or lost its acknowledgement", duplicate, err)
	}
	q.ID = newID()
	stale, err := Deliver(context.Background(), project, "session", q, false)
	if err != nil || stale.Status != "conflicted" || applied.Load() != 1 {
		t.Fatal("stale version applied", stale, err)
	}
	stop()
	if c.Values.TaskConcurrency != 5 || c.Values.Revision != 1 {
		t.Fatal("acknowledgement preceded application")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := New(project, "session", FromConfig(config.Defaults()), func(_ Request, _ Values, _ map[string]any) error { applied.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.Generation == c.Generation || next.Values.TaskConcurrency != config.Defaults().TaskConcurrency {
		t.Fatal("replacement inherited the prior process override")
	}
	old, err := Deliver(context.Background(), project, "session", q, false)
	if err != nil || old.Status != "rejected" || applied.Load() != 1 {
		t.Fatal("old generation reached its replacement", old, err)
	}
}

func TestRuntimeExpiredAndUncertainRequestsNeverApply(t *testing.T) {
	called := 0
	c, err := New(t.TempDir(), "session", FromConfig(config.Defaults()), func(_ Request, _ Values, _ map[string]any) error { called++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	q := requestFor(t, c, 5)
	q.Expires = time.Now().Add(-time.Second)
	if err := writeCommitted(c.pending, q.ID, q); err != nil {
		t.Fatal(err)
	}
	if err := c.Poll(); err != nil {
		t.Fatal(err)
	}
	out, err := Deliver(context.Background(), c.Project, c.Session, q, true)
	if err != nil || out.Status != "expired" || called != 0 {
		t.Fatal("expired request applied", out, err)
	}
	q = requestFor(t, c, 5)
	if err := writeCommitted(c.started, q.ID, q); err != nil {
		t.Fatal(err)
	}
	if err := writeCommitted(c.pending, q.ID, q); err != nil {
		t.Fatal(err)
	}
	if err := c.Poll(); err != nil {
		t.Fatal(err)
	}
	out, err = Deliver(context.Background(), c.Project, c.Session, q, true)
	if err != nil || out.Status != "unknown" || called != 0 {
		t.Fatal("uncertain request reapplied", out, err)
	}
	q = requestFor(t, c, 5)
	q.Grant.Classes = nil
	if err := c.process(q); err != nil {
		t.Fatal(err)
	}
	if err := readCommitted(c.outcomes, q.ID, &out); err != nil || out.Status != "rejected" || called != 0 {
		t.Fatal("ungranted preference applied", out, err)
	}
}

func TestRuntimeStatusOrderingSurvivesClockRollback(t *testing.T) {
	c, err := New(t.TempDir(), "session", FromConfig(config.Defaults()), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.statusSerial = time.Now().Add(time.Hour).UnixNano()
	c.Values.TaskConcurrency = 7
	if err := c.heartbeat(true); err != nil {
		t.Fatal(err)
	}
	c.Values.TaskConcurrency = 8
	if err := c.heartbeat(true); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStatus(c.Project, c.Session)
	if err != nil || got.Values.TaskConcurrency != 8 {
		t.Fatal("wall-clock ordering hid current status", got, err)
	}
}

func TestRuntimeStatusRecoversUnfinishedHeartbeats(t *testing.T) {
	project := t.TempDir()
	root, err := openDirectory(project, "session", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for i := 0; i < 80; i++ {
		name := fmt.Sprintf("%020d-%s.json", i, newID())
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(`{"incomplete":`)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := New(project, "session", FromConfig(config.Defaults()), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if status, err := ReadStatus(project, "session"); err != nil || status.Generation != c.Generation {
		t.Fatal("partial status files blocked recovery", status, err)
	}
}

func TestRuntimeFilesRejectEscapesSpecialFilesAndPartialCommits(t *testing.T) {
	project, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(project, ".agents")); err != nil {
		t.Fatal(err)
	}
	if _, err := New(project, "session", Values{}, nil); err == nil {
		t.Fatal("created control state outside project")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("escape wrote outside project")
	}
	project = t.TempDir()
	root, err := openDirectory(project, "session", true, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := root.OpenFile("partial.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte(`{"incomplete":`))
	_ = f.Close()
	if ids, err := committedIDs(root, 10); err != nil || len(ids) != 0 {
		t.Fatal("incomplete data became visible", ids, err)
	}
	if err := writeCommitted(root, "partial", map[string]any{"different": true}); err == nil {
		t.Fatal("replaced incomplete identity with unrelated data")
	}
	if err := syscall.Mkfifo(filepath.Join(root.Name(), "pipe.json"), 0600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { var v any; result <- readObject(root, "pipe.json", &v) }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("read a FIFO as control data")
		}
	case <-time.After(time.Second):
		t.Fatal("control reader blocked on a FIFO")
	}
}
