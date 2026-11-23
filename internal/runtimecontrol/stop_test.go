package runtimecontrol

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
)

func stopRequest(t *testing.T, c *Controller, grant control.Grant) Request {
	t.Helper()
	view, err := View(c.Project, c.Session, c.Generation, c.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: StopOperation, SchemaVersion: Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation, Parameters: map[string]any{}}
	raw, _ := json.Marshal(p)
	return Request{ID: newID(), UserID: "fixture-user", Proposal: p, Digest: artifact.Digest(raw), Expires: time.Now().Add(2 * time.Second), Grant: grant}
}

func TestRunningSessionOffersStopInItsOwnWords(t *testing.T) {
	values := FromConfig(config.Defaults())
	view, err := View(t.TempDir(), "session", "g", values, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Descriptor.Actions) != 1 || view.Descriptor.Actions[0].Operation != StopOperation || view.Descriptor.Actions[0].Label != "Stop session" {
		t.Fatalf("actions: %+v", view.Descriptor.Actions)
	}
	if d := view.Descriptor.Actions[0].Description; !strings.Contains(d, "A reply here resumes the session") || strings.Contains(d, "stops with it") {
		t.Fatalf("description: %q", d)
	}
	values.Relays = map[string]RelayRoute{"claude": {Agent: "claude"}}
	view, _ = View(t.TempDir(), "session", "g", values, true)
	if d := view.Descriptor.Actions[0].Description; !strings.Contains(d, "Claude Code stops with it.") {
		t.Fatalf("relay not named: %q", d)
	}
	grant, _ := Grant(t.TempDir(), "session")
	if err := view.Descriptor.Validate(grant); err != nil {
		t.Fatalf("descriptor with stop: %v", err)
	}
	// A session that is not running offers nothing to stop.
	view, _ = View(t.TempDir(), "session", "g", values, false)
	if len(view.Descriptor.Actions) != 0 {
		t.Fatalf("stopped runtime offers %+v", view.Descriptor.Actions)
	}
}

func TestStopIsAcknowledgedThenTheRuntimeReportsItselfGone(t *testing.T) {
	project := t.TempDir()
	var stops atomic.Int32
	c, err := New(project, "session", FromConfig(config.Defaults()), func(Request, Values, map[string]any) error { t.Error("a stop is not a settings change"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	c.Stop = func(Request) error { stops.Add(1); return nil }
	full, _ := Grant(project, "session")
	// Without the operation in its grant the stop is refused before it is queued.
	if out, err := Deliver(context.Background(), project, "session", stopRequest(t, c, WithoutStop(full)), false); err != nil || out.Status != "conflicted" {
		t.Fatalf("ungranted stop: %+v %v", out, err)
	}
	q := stopRequest(t, c, full)
	stop := runLoop(t, c)
	out, err := Deliver(context.Background(), project, "session", q, false)
	stop()
	if err != nil || out.Status != "succeeded" || out.Result["runtime_applied"] != true || stops.Load() != 1 {
		t.Fatalf("stop: %+v %v (stops %d)", out, err, stops.Load())
	}
	status, err := ReadStatus(project, "session")
	if err != nil || status.AvailableUntil.After(time.Now()) {
		t.Fatalf("runtime still reported available after the stop: %+v %v", status, err)
	}
	// The same command again is answered from the record, not repeated.
	again, err := Deliver(context.Background(), project, "session", q, true)
	if err != nil || again.Status != "succeeded" || stops.Load() != 1 {
		t.Fatalf("duplicate: %+v %v", again, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStopIsRefusedByARuntimeThatCannotStop(t *testing.T) {
	project := t.TempDir()
	c, err := New(project, "session", FromConfig(config.Defaults()), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	full, _ := Grant(project, "session")
	q := stopRequest(t, c, full)
	stop := runLoop(t, c)
	out, err := Deliver(context.Background(), project, "session", q, false)
	stop()
	if err != nil || out.Status != "rejected" {
		t.Fatalf("stop without a handler: %+v %v", out, err)
	}
	if status, _ := ReadStatus(project, "session"); !status.AvailableUntil.After(time.Now()) {
		t.Fatal("a refused stop marked the runtime unavailable")
	}
}
