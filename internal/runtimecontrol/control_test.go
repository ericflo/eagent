package runtimecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestApplyValuesRoutes(t *testing.T) {
	project := t.TempDir()
	c, err := New(project, "session", FromConfig(config.Defaults()), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	view, err := View(project, "session", c.Generation, c.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := Grant(project, "session")
	proposal := func(edits ...control.Edit) control.Proposal {
		p := control.Proposal{Operation: "settings.apply", SchemaVersion: Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation, Edits: edits}
		return p
	}
	routeEdits := func(provider, model, effort string) []control.Edit {
		return []control.Edit{
			{Op: "set", Key: "/routes/orchestrator/provider", Value: provider},
			{Op: "set", Key: "/routes/orchestrator/model", Value: model},
			{Op: "set", Key: "/routes/orchestrator/effort", Value: effort},
		}
	}
	// A pick whose key is not set where the session runs is rejected.
	t.Setenv("OPENROUTER_API_KEY", "")
	if _, err := ApplyValues(project, c.Values, proposal(routeEdits("openrouter", "reflection/beam-1.0-501b-a23b", "xhigh")...), view, grant); err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("keyless pick: %v", err)
	}
	t.Setenv("OPENROUTER_API_KEY", "test")
	// A valid change is accepted and recorded.
	out, err := ApplyValues(project, c.Values, proposal(routeEdits("openrouter", "reflection/beam-1.0-501b-a23b", "xhigh")...), view, grant)
	if err != nil {
		t.Fatal(err)
	}
	if out.Routes["orchestrator"] != (Route{Provider: "openrouter", Model: "reflection/beam-1.0-501b-a23b", Effort: "xhigh"}) {
		t.Fatalf("routes = %+v", out.Routes)
	}
	// Untouched actors keep their initial routes.
	if out.Routes["task"].Model != c.Values.Routes["task"].Model {
		t.Fatalf("untouched actor changed: %+v", out.Routes["task"])
	}
	// Unknown model at a known provider is rejected.
	if _, err := ApplyValues(project, c.Values, proposal(routeEdits("openrouter", "nope/nothing", "low")...), view, grant); err == nil {
		t.Fatal("unknown model accepted")
	}
	// Unknown provider is rejected by descriptor enum validation.
	if _, err := ApplyValues(project, c.Values, proposal(routeEdits("evil", "x", "low")...), view, grant); err == nil {
		t.Fatal("unknown provider accepted")
	}
	// Effort the model does not list is rejected (glm-5.3-flash has no max).
	if _, err := ApplyValues(project, c.Values, proposal(routeEdits("openrouter", "z-ai/glm-5.3-flash", "max")...), view, grant); err == nil {
		t.Fatal("unlisted effort accepted")
	}
	// Unset on a route field is rejected.
	if _, err := ApplyValues(project, c.Values, proposal(control.Edit{Op: "unset", Key: "/routes/orchestrator/model"}), view, grant); err == nil {
		t.Fatal("unset of route field accepted")
	}
	// Unknown actor is rejected.
	if _, err := ApplyValues(project, c.Values, proposal(control.Edit{Op: "set", Key: "/routes/root/model", Value: "x"}), view, grant); err == nil {
		t.Fatal("unknown actor accepted")
	}
	// The view exposes the route fields in the route class, writable when available.
	routeFields := 0
	for _, f := range view.Descriptor.Fields {
		if strings.HasPrefix(f.Key, "/routes/") {
			routeFields++
			if f.Class != "route" || !f.Writable {
				t.Fatalf("route field %s: class=%s writable=%v", f.Key, f.Class, f.Writable)
			}
		}
	}
	if routeFields != 9 {
		t.Fatalf("route fields = %d, want 9", routeFields)
	}
	if view.Snapshot.Saved["/routes/orchestrator/model"] != c.Values.Routes["orchestrator"].Model {
		t.Fatalf("saved route = %v", view.Snapshot.Saved["/routes/orchestrator/model"])
	}
}

// Choosing a configuration sets every actor's route to what it runs here,
// and route edits in the same proposal apply on top of it.
func TestApplyValuesConfiguration(t *testing.T) {
	project := t.TempDir()
	c, err := New(project, "session", FromConfig(config.Defaults()), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Values.Config != "glm" {
		t.Fatalf("defaults start as %q", c.Values.Config)
	}
	view, err := View(project, "session", c.Generation, c.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	if view.Snapshot.Effective["/config"] != "glm" || view.Descriptor.Fields[0].Key != "/config" {
		t.Fatalf("configuration = %v, first field %s", view.Snapshot.Effective["/config"], view.Descriptor.Fields[0].Key)
	}
	grant, _ := Grant(project, "session")
	proposal := func(edits ...control.Edit) control.Proposal {
		return control.Proposal{Operation: "settings.apply", SchemaVersion: Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation, Edits: edits}
	}
	choose := control.Edit{Op: "set", Key: "/config", Value: "anthropic-high"}
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := ApplyValues(project, c.Values, proposal(choose), view, grant); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("keyless configuration: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test")
	out, err := ApplyValues(project, c.Values, proposal(choose), view, grant)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := config.LoadBundle(project, "anthropic-high", "")
	if out.Config != "anthropic-high" || !reflect.DeepEqual(out.Routes, FromConfig(want).Routes) {
		t.Fatalf("after anthropic-high: %+v", out)
	}
	if c.Values.Routes["orchestrator"].Model == out.Routes["orchestrator"].Model {
		t.Fatal("the controller's own routes changed under the proposal")
	}
	out, err = ApplyValues(project, c.Values, proposal(control.Edit{Op: "set", Key: "/routes/narrator/model", Value: "claude-opus-5-5"}, choose), view, grant)
	if err != nil {
		t.Fatal(err)
	}
	if out.Routes["narrator"].Model != "claude-opus-5-5" || out.Routes["task"] != FromConfig(want).Routes["task"] {
		t.Fatalf("a change on top: %+v", out.Routes)
	}
	if _, err := ApplyValues(project, c.Values, proposal(control.Edit{Op: "set", Key: "/config", Value: "no-such"}), view, grant); err == nil {
		t.Fatal("unknown configuration accepted")
	}
}

func TestResolveLiveModelStaticAndCached(t *testing.T) {
	project := t.TempDir()
	// Static catalog hit needs no cache.
	if _, _, ok := config.ResolveLiveModel(project, "openrouter", "z-ai/glm-5.3"); !ok {
		t.Fatal("static catalog model not resolved")
	}
	// A live-only model resolves from the cache.
	if err := config.WriteLiveCache(project, &config.LiveCache{FetchedAt: time.Now(), Models: []config.Model{{Provider: "openrouter", ID: "acme/nova-9", Label: "Nova 9", Live: true}}}); err != nil {
		t.Fatal(err)
	}
	m, pr, ok := config.ResolveLiveModel(project, "openrouter", "acme/nova-9")
	if !ok || m.ID != "acme/nova-9" || pr.ID != "openrouter" {
		t.Fatalf("live model: %+v %+v %v", m, pr, ok)
	}
	if _, _, ok := config.ResolveLiveModel(project, "openrouter", "acme/missing"); ok {
		t.Fatal("missing model resolved")
	}
	if _, _, ok := config.ResolveLiveModel(project, "nope", "z-ai/glm-5.3"); ok {
		t.Fatal("unknown provider resolved")
	}
}
