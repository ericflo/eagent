package harness

import (
	"context"
	"encoding/json"
	"github.com/ericflo/eagent/internal/config"
	"reflect"
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
	if r2.runtimeControl.Generation == status.Generation || !reflect.DeepEqual(r2.activeSettings, runtimecontrol.FromConfig(cfg)) {
		t.Fatal("resumed runtime retained old generation or overrides")
	}
	q.ID = "stale-runtime"
	if outcome, err := runtimecontrol.Deliver(context.Background(), project, r.sess.ID, q, false); err != nil || outcome.Status != "rejected" {
		t.Fatalf("replacement accepted old command: %+v %v", outcome, err)
	}
}

func TestRouteChangesSwapOnlyChangedPicksAndKeepFallbacks(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("OPENROUTER_API_KEY", "x")
	t.Setenv("ANTHROPIC_API_KEY", "")
	project := t.TempDir()
	cfg := testConfig("http://127.0.0.1:1") // dead model server; clients are swapped, not called
	cfg.Orchestrator.Fallback = &config.Actor{Protocol: "openai-chat", BaseURL: "http://127.0.0.1:2", Model: "orch-fallback", APIKeyEnv: "EAGENT_TEST_KEY"}
	cfg.Orchestrator.MaxTokens = 32768
	rt, err := New(cfg, Options{Project: project, Interactive: false}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	before := runtimecontrol.FromConfig(cfg).Routes
	routeEvents := func() map[string]string {
		evs, _ := store.Read(rt.sess.Path)
		out := map[string]string{}
		for _, ev := range evs {
			if ev.Type == event.Route {
				var d event.RouteData
				_ = ev.Decode(&d)
				out[d.Actor] = d.Model
			}
		}
		return out
	}

	// Unchanged picks swap nothing: an actor that failed over to its
	// fallback stays there when an unrelated setting changes.
	rt.orchClient = rt.newClient(rt.orchRoutes[1], event.ActorOrchestrator)
	swaps, err := rt.routeChanges(before, before)
	if err != nil || len(swaps) != 0 {
		t.Fatalf("unchanged picks: %v %v", swaps, err)
	}

	// A new model replaces the route and drops the old model's fallback,
	// keeping the configured token limit.
	after := map[string]runtimecontrol.Route{}
	for k, v := range before {
		after[k] = v
	}
	after["task"] = runtimecontrol.Route{Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "high"}
	swaps, err = rt.routeChanges(before, after)
	if err != nil || len(swaps) != 1 || swaps[0].actor != "task" {
		t.Fatalf("task pick: %+v %v", swaps, err)
	}
	rt.swapRoute(swaps[0])
	if ep := rt.taskClient.Endpoint; ep.Model != "z-ai/glm-5.3" || ep.BaseURL != "https://openrouter.ai/api/v1" || ep.ReasoningEffort != "high" || len(rt.taskRoutes) != 1 {
		t.Fatalf("task endpoint = %+v routes=%d", ep, len(rt.taskRoutes))
	}
	if routeEvents()["task"] != "z-ai/glm-5.3" {
		t.Fatalf("route events = %v", routeEvents())
	}

	// Only the effort changes: same model, its fallback chain stays.
	rt.cfg.Orchestrator.BaseURL = "https://openrouter.ai/api/v1" // a catalog provider, so the pick resolves
	rt.cfg.Orchestrator.Model = "z-ai/glm-5.3"
	after = map[string]runtimecontrol.Route{"orchestrator": {Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "low"}}
	swaps, err = rt.routeChanges(map[string]runtimecontrol.Route{"orchestrator": {Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "high"}}, after)
	if err != nil || len(swaps) != 1 {
		t.Fatalf("effort pick: %+v %v", swaps, err)
	}
	if len(swaps[0].eps) != 2 || swaps[0].eps[0].ReasoningEffort != "low" || swaps[0].eps[0].MaxTokens != 32768 || swaps[0].eps[1].Model != "orch-fallback" {
		t.Fatalf("effort change lost the fallback or limits: %+v", swaps[0].eps)
	}

	// A pick that cannot run fails the whole change before anything applies.
	for _, bad := range []runtimecontrol.Route{
		{Provider: "openrouter", Model: "nope/none", Effort: "low"},
		{Provider: "anthropic", Model: "claude-opus-5-5", Effort: "high"}, // ANTHROPIC_API_KEY unset
	} {
		if _, err := rt.routeChanges(before, map[string]runtimecontrol.Route{"narrator": bad}); err == nil {
			t.Fatalf("unusable pick %+v accepted", bad)
		}
	}
}
