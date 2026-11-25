package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/state"
)

// Ports of the old settings_test.go: live runtime settings.

// coreControlUp waits until a session's runtime control is serving.
func coreControlUp(t *testing.T, project, session string) runtimecontrol.Status {
	t.Helper()
	var status runtimecontrol.Status
	waitFor(t, "the runtime control", func() bool {
		var err error
		status, err = runtimecontrol.ReadStatus(project, session)
		return err == nil && status.AvailableUntil.After(time.Now())
	})
	return status
}

// coreRequest is a command to a running session's settings, as the app
// sends it through the connector.
func coreRequest(t *testing.T, project, session, id, operation string, edits ...control.Edit) runtimecontrol.Request {
	t.Helper()
	current, err := runtimecontrol.ReadStatus(project, session)
	if err != nil {
		t.Fatal(err)
	}
	view, err := runtimecontrol.View(project, session, current.Generation, current.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := runtimecontrol.Grant(project, session)
	p := control.Proposal{Operation: operation, SchemaVersion: runtimecontrol.Schema, Generation: current.Generation, ExpectedVersion: view.Snapshot.Version, Edits: edits}
	if operation == runtimecontrol.StopOperation {
		p.Parameters = map[string]any{}
	}
	raw, _ := json.Marshal(p)
	return runtimecontrol.Request{ID: id, UserID: "test-owner", Digest: artifact.Digest(raw), Expires: time.Now().Add(10 * time.Second), Proposal: p, Grant: grant}
}

// Old TestLiveSettingsReachSchedulerAndNativeHistory: a change from the app
// is applied to the running session and acknowledged only once it is in the
// session's log, exactly once; the same command again is not applied twice;
// the configuration it started from is not touched; the timer it changes is
// rescheduled so an old one cannot fire; and a resumed session starts from
// the configuration again, under a new generation an old command cannot
// reach. (The worker count it changed, task_concurrency, is gone with task
// workers; the narrator's tick and quiet limit are now the loop's.)
func TestCoreLiveSettingsReachTheLoopAndEndWithTheRun(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	project, id := s.rt.opts.Project, s.rt.sess.ID
	first := coreControlUp(t, project, id)
	cfg := s.rt.cfg
	var genBefore uint64
	s.rt.sync(func() { genBefore = s.rt.turns.tickGen })

	q := coreRequest(t, project, id, "retime", "settings.apply",
		control.Edit{Op: "set", Key: "/tick_seconds", Value: float64(120)},
		control.Edit{Op: "set", Key: "/quiet_seconds", Value: float64(0)})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := runtimecontrol.Deliver(ctx, project, id, q, false)
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("runtime acknowledgement: %+v %v", result, err)
	}
	if n := len(s.events(runtimecontrol.EventType)); n != 1 {
		t.Fatalf("acknowledged with %d durable events", n)
	}
	if dup, err := runtimecontrol.Deliver(ctx, project, id, q, false); err != nil || dup.Result["version"] != result.Result["version"] {
		t.Fatalf("duplicate: %+v %v", dup, err)
	}
	if n := len(s.events(runtimecontrol.EventType)); n != 1 {
		t.Fatalf("the duplicate was applied again: %d events", n)
	}
	s.rt.sync(func() {
		if s.rt.activeSettings.TickSeconds != 120 || s.rt.activeSettings.QuietSeconds != 0 || s.rt.quietLimit() != 0 {
			t.Errorf("not applied: %+v", s.rt.activeSettings)
		}
		if s.rt.cfg.TickSeconds != cfg.TickSeconds || s.rt.cfg.QuietSeconds != cfg.QuietSeconds {
			t.Error("live overrides mutated the configuration")
		}
		if s.rt.turns.tickGen <= genBefore {
			t.Error("the progress timer was not rescheduled, so the old one can still fire")
		}
	})

	path := s.rt.sess.Path
	s.stop()
	s2, _ := coreResume(t, path, project, s.model, newFakeSeat("unused"))
	second := coreControlUp(t, project, id)
	if second.Generation == first.Generation {
		t.Fatal("the resumed session kept the old generation")
	}
	var values runtimecontrol.Values
	s2.rt.sync(func() { values = s2.rt.activeSettings })
	if want := runtimecontrol.FromConfigFor(project, cfg); values.TickSeconds != want.TickSeconds || values.QuietSeconds != want.QuietSeconds {
		t.Fatalf("the resumed session kept the overrides: %+v", values)
	}
	q.ID = "stale-runtime"
	if outcome, err := runtimecontrol.Deliver(ctx, project, id, q, false); err != nil || outcome.Status != "rejected" {
		t.Fatalf("the new run accepted an old command: %+v %v", outcome, err)
	}
}

// Old TestRouteChangesSwapOnlyChangedPicksAndKeepFallbacks: a settings
// change swaps only the model whose pick changed (one that failed over to
// its fallback stays there when something else changes); a new model drops
// the old model's fallback; an effort change keeps the fallback chain and
// the configured limits; a pick that cannot run fails the whole change.
// (The orchestrator, task and narrator routes are now eagent's one route.)
func TestCoreRouteChangesSwapOnlyChangedPicksAndKeepFallbacks(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("OPENROUTER_API_KEY", "x")
	t.Setenv("ANTHROPIC_API_KEY", "")
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1") // clients are swapped, never called
	cfg.Model.Fallback = &config.ModelRoute{Protocol: "openai-chat", BaseURL: "http://127.0.0.1:2", Model: "eagent-fallback", APIKeyEnv: "EAGENT_TEST_KEY"}
	cfg.Model.MaxTokens = 32768
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	before := runtimecontrol.FromConfig(cfg).Routes
	eagent := rt.models[event.ActorEagent]
	if len(eagent.routes) != 2 {
		t.Fatalf("routes = %+v", eagent.routes)
	}
	// Failed over to the fallback: an unrelated change leaves it there.
	eagent.client = rt.newClient(eagent.routes[1], event.ActorEagent)
	if swaps, err := rt.routeChanges(before, before); err != nil || len(swaps) != 0 {
		t.Fatalf("unchanged picks: %v %v", swaps, err)
	}
	// A new model replaces the route and drops the old model's fallback.
	after := map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: {Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "high"}}
	swaps, err := rt.routeChanges(before, after)
	if err != nil || len(swaps) != 1 || swaps[0].who != runtimecontrol.RouteEagent {
		t.Fatalf("new pick: %+v %v", swaps, err)
	}
	rt.swapRoute(swaps[0])
	if ep := rt.models[event.ActorEagent].client.Endpoint; ep.Model != "z-ai/glm-5.3" || ep.BaseURL != "https://openrouter.ai/api/v1" || ep.ReasoningEffort != "high" || len(rt.models[event.ActorEagent].routes) != 1 {
		t.Fatalf("endpoint = %+v routes=%d", ep, len(rt.models[event.ActorEagent].routes))
	}
	routed := ""
	for _, ev := range mustReadCore(t, rt.sess.Path) {
		if ev.Type == event.Route {
			var d event.RouteData
			_ = ev.Decode(&d)
			routed = d.Actor + " " + d.Model
		}
	}
	if routed != runtimecontrol.RouteEagent+" z-ai/glm-5.3" {
		t.Fatalf("route event = %q", routed)
	}
	// Only the effort changes: same model, its fallback chain and limits stay.
	rt.cfg.Model.BaseURL = "https://openrouter.ai/api/v1"
	rt.cfg.Model.Model = "z-ai/glm-5.3"
	swaps, err = rt.routeChanges(
		map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: {Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "high"}},
		map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: {Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "low"}})
	if err != nil || len(swaps) != 1 {
		t.Fatalf("effort pick: %+v %v", swaps, err)
	}
	if eps := swaps[0].eps; len(eps) != 2 || eps[0].ReasoningEffort != "low" || eps[0].MaxTokens != 32768 || eps[1].Model != "eagent-fallback" {
		t.Fatalf("an effort change lost the fallback or the limits: %+v", eps)
	}
	for _, bad := range []runtimecontrol.Route{
		{Provider: "openrouter", Model: "nope/none", Effort: "low"},
		{Provider: "anthropic", Model: "claude-opus-5-5", Effort: "high"}, // ANTHROPIC_API_KEY unset
	} {
		if _, err := rt.routeChanges(before, map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: bad}); err == nil {
			t.Fatalf("unusable pick %+v accepted", bad)
		}
	}
}

// Old TestRemoteStopEndsTheSession: a stop from the app ends the session as
// the web UI's Stop does, after the session has recorded and acknowledged
// it.
func TestCoreRemoteStopEndsTheSession(t *testing.T) {
	s := startSession(t, "Wait for me", func(string, []map[string]any) reply { return hold() }, nil)
	project, id := s.rt.opts.Project, s.rt.sess.ID
	coreControlUp(t, project, id)
	// The fake agent's events must be in before it is killed (fakeSeat
	// closes its channel on Kill).
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := runtimecontrol.Deliver(ctx, project, id, coreRequest(t, project, id, "stop-from-phone", runtimecontrol.StopOperation), false)
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("stop: %+v %v", result, err)
	}
	if code := coreExit(s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	requested, reason := false, ""
	for _, e := range mustReadCore(t, s.rt.sess.Path) {
		switch e.Type {
		case "session.stop_requested":
			requested = true
		case event.SessionEnd:
			var d event.SessionEndData
			_ = json.Unmarshal(e.Data, &d)
			reason = d.Reason
		}
	}
	if !requested || reason != "quit" {
		t.Fatalf("stop recorded %v, session ended with %q", requested, reason)
	}
}

// Old TestStopSessionStopsItsRelays: stopping the session, from the app or
// from the web UI, stops its agent too (its terminal is killed and its end
// recorded), and the app's Stop says so before the user confirms.
func TestCoreStopSessionStopsItsAgent(t *testing.T) {
	for _, from := range []string{"app", "web"} {
		t.Run(from, func(t *testing.T) {
			s := startSession(t, "Look at the open issues", func(string, []map[string]any) reply { return hold() }, func(cfg *config.Config) { cfg.Relay.Default = "claude" })
			project, id := s.rt.opts.Project, s.rt.sess.ID
			status := coreControlUp(t, project, id)
			s.waitFor("the agent", func() bool {
				var active int
				s.read(func(st *state.State) { active = len(st.ActiveRelays()) })
				return active == 1 && s.rt.relays.get("claude") != nil && len(s.events(event.RelayTurn)) == 1
			})
			s.settled()
			if from == "web" {
				if err := PostInbox(s.rt.sess.Path, InboxMessage{Type: "stop", From: "web"}); err != nil {
					t.Fatal(err)
				}
			} else {
				status, _ = runtimecontrol.ReadStatus(project, id)
				view, err := runtimecontrol.View(project, id, status.Generation, status.Values, true)
				if err != nil {
					t.Fatal(err)
				}
				described := false
				for _, a := range view.Descriptor.Actions {
					described = described || a.Operation == runtimecontrol.StopOperation && strings.Contains(a.Description, "Claude Code stops with it.")
				}
				if !described {
					t.Fatalf("stop does not say the agent stops: %+v", view.Descriptor.Actions)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if result, err := runtimecontrol.Deliver(ctx, project, id, coreRequest(t, project, id, "stop-relay-session", runtimecontrol.StopOperation), false); err != nil || result.Status != "succeeded" {
					t.Fatalf("stop: %+v %v", result, err)
				}
			}
			coreExit(s)
			if !s.seat.wasKilled() {
				t.Fatal("the agent was left running")
			}
			ended := false
			for _, e := range mustReadCore(t, s.rt.sess.Path) {
				if e.Type == event.RelayEnd {
					var d event.RelayEndData
					_ = json.Unmarshal(e.Data, &d)
					ended = ended || d.Name == "claude" && d.Reason == "the user stopped the session"
				}
			}
			if !ended {
				t.Fatal("the agent's end was not recorded")
			}
		})
	}
}
