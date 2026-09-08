package harness

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/store"
)

func TestLiveSettingsReachSchedulerAndNativeHistory(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	blocked := make(chan struct{})
	s := newScripted(func(model string, msgs []map[string]any) reply {
		if model == "orch" && countRole(msgs, "assistant") == 0 {
			return reply{calls: []event.ToolCall{tc("delegate", `{"title":"One","description":"Wait"}`), tc("delegate", `{"title":"Two","description":"Wait"}`), tc("delegate", `{"title":"Three","description":"Wait"}`)}}
		}
		if model == "narr" {
			return reply{text: "The workers are running."}
		}
		return reply{block: blocked}
	})
	defer s.srv.Close()
	project := t.TempDir()
	cfg := testConfig(s.srv.URL)
	cfg.TaskConcurrency = 1
	r, err := New(cfg, Options{Project: project, Prompt: "Start three tasks", Interactive: true}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("runtime test timed out")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	wait(func() bool {
		ready := false
		r.sync(func() { ready = len(r.st.Tasks) == 3 && len(r.running) == 1 })
		return ready
	})
	grant, _ := runtimecontrol.Grant(project, r.sess.ID)
	status, err := runtimecontrol.ReadStatus(project, r.sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := func(id string, edits ...control.Edit) runtimecontrol.Request {
		t.Helper()
		current, err := runtimecontrol.ReadStatus(project, r.sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		view, err := runtimecontrol.View(project, r.sess.ID, current.Generation, current.Values, true)
		if err != nil {
			t.Fatal(err)
		}
		p := control.Proposal{Operation: "settings.apply", SchemaVersion: runtimecontrol.Schema, Generation: current.Generation, ExpectedVersion: view.Snapshot.Version, Edits: edits}
		raw, _ := json.Marshal(p)
		return runtimecontrol.Request{ID: id, UserID: "test-owner", Digest: artifact.Digest(raw), Expires: time.Now().Add(10 * time.Second), Proposal: p, Grant: grant}
	}
	q := request("raise-workers", control.Edit{Op: "set", Key: "/task_concurrency", Value: float64(2)})
	result, err := runtimecontrol.Deliver(ctx, project, r.sess.ID, q, false)
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("runtime acknowledgement: %+v %v", result, err)
	}
	wait(func() bool { running := 0; r.sync(func() { running = len(r.running) }); return running == 2 })
	// Success must already exist in the native log, through its normal writer.
	events, err := store.Read(r.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	changes := 0
	for _, e := range events {
		if e.Type == runtimecontrol.EventType {
			changes++
		}
	}
	if changes != 1 {
		t.Fatalf("acknowledged without exactly one durable event: %d", changes)
	}
	if duplicate, err := runtimecontrol.Deliver(ctx, project, r.sess.ID, q, false); err != nil || duplicate.Result["version"] != result.Result["version"] {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	q = request("lower-and-retime", control.Edit{Op: "set", Key: "/task_concurrency", Value: float64(1)}, control.Edit{Op: "set", Key: "/narrator_tick_seconds", Value: float64(120)}, control.Edit{Op: "set", Key: "/narrator_quiet_seconds", Value: float64(0)})
	var timerBefore uint64
	r.sync(func() { timerBefore = r.narrTimerGeneration })
	if result, err := runtimecontrol.Deliver(ctx, project, r.sess.ID, q, false); err != nil || result.Status != "succeeded" {
		t.Fatalf("lower: %+v %v", result, err)
	}
	r.sync(func() {
		if len(r.running) != 2 || r.st.Tasks["t3"].Status != "queued" {
			t.Error("lowering cancelled an active worker or admitted the third")
		}
		if r.cfg.TaskConcurrency != 1 || r.cfg.NarratorTickSeconds != 3600 || r.cfg.NarratorQuietSeconds != cfg.NarratorQuietSeconds {
			t.Error("live overrides mutated shared defaults")
		}
		if r.activeSettings.NarratorQuietSeconds != 0 || r.narrTimerGeneration <= timerBefore {
			t.Error("narrator settings were not applied")
		}
		generation, pending := r.narrTimerGeneration, r.narrPending
		r.narratorTimerFired(timerBefore)
		if r.narrTimerGeneration != generation || r.narrPending != pending {
			t.Error("an old queued timer fired after rescheduling")
		}
	})
	// Shut down and resume from the same native session. Overrides do not leak
	// into a replacement generation, and an old command cannot reach it.
	cancel()
	<-done
	r2, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{}, r.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	r2.startRuntimeSettings()
	defer func() { r2.stopRuntimeSettings(); r2.beginShutdown("test", 0); r2.finish() }()
	if r2.runtimeControl.Generation == status.Generation || r2.activeSettings != runtimecontrol.FromConfig(cfg) {
		t.Fatal("resumed runtime retained old generation or overrides")
	}
	q.ID = "stale-runtime"
	if outcome, err := runtimecontrol.Deliver(context.Background(), project, r.sess.ID, q, false); err != nil || outcome.Status != "rejected" {
		t.Fatalf("replacement accepted old command: %+v %v", outcome, err)
	}
}
