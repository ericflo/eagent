package harness

import (
	"fmt"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

func (r *Runtime) startRuntimeSettings() {
	c, err := runtimecontrol.New(r.opts.Project, r.sess.ID, r.activeSettings, r.applyRuntimeSettings)
	if err != nil {
		r.ui.Log("live settings unavailable: %v", err)
		return
	}
	r.runtimeControl = c
	r.append(event.New("settings.runtime_started", event.ActorHarness, map[string]any{"format": runtimecontrol.Schema, "generation": c.Generation, "values": r.activeSettings, "persistence": "this process only"}))
}

func (r *Runtime) stopRuntimeSettings() {
	if r.runtimeControl != nil {
		if err := r.runtimeControl.Close(); err != nil {
			r.ui.Log("live settings shutdown: %v", err)
		}
		r.runtimeControl = nil
	}
}

func (r *Runtime) pollRuntimeSettings() {
	if r.runtimeControl != nil && !r.ending {
		if err := r.runtimeControl.Poll(); err != nil {
			r.ui.Log("live settings: %v", err)
		}
	}
}

func (r *Runtime) applyRuntimeSettings(q runtimecontrol.Request, values runtimecontrol.Values, result map[string]any) error {
	if r.ending || r.ctx.Err() != nil || !q.Expires.After(time.Now()) || r.runtimeControl == nil || q.Proposal.Generation != r.runtimeControl.Generation {
		return fmt.Errorf("the targeted runtime stopped before applying the change")
	}
	// Resolve every changed route before recording anything, so a route
	// that cannot run fails the change instead of being reported as applied.
	swaps, err := r.routeChanges(r.activeSettings.Routes, values.Routes)
	if err != nil {
		return err
	}
	previousSeq := r.st.LastSeq()
	ev := r.append(event.New(runtimecontrol.EventType, event.ActorHarness, map[string]any{
		"format": runtimecontrol.Schema, "command_id": q.ID, "user_id": q.UserID, "proposal_sha256": q.Digest,
		"generation": q.Proposal.Generation, "resource_key": q.Grant.Key, "previous": r.activeSettings, "values": values, "result": result,
	}))
	if ev.Seq <= previousSeq || ev.Seq != r.st.LastSeq() {
		return fmt.Errorf("the settings event was not durably recorded")
	}
	changedTimer := r.activeSettings.NarratorTickSeconds != values.NarratorTickSeconds
	r.activeSettings = values
	if changedTimer {
		r.armNarratorTimer()
	}
	for _, sw := range swaps {
		r.swapRoute(sw)
	}
	return nil
}

// routeSwap is one actor's new route list, resolved from a settings pick.
type routeSwap struct {
	actor string
	pick  runtimecontrol.Route
	eps   []llm.Endpoint
}

// routeChanges resolves the actors whose pick changed. An actor whose pick
// did not change keeps its routes, including a fallback it switched to. A
// changed pick starts from the configured actor (see config.PickRoute:
// limits stay, another model drops the old model's fallback chain).
func (r *Runtime) routeChanges(before, after map[string]runtimecontrol.Route) ([]routeSwap, error) {
	var out []routeSwap
	for _, actor := range []string{event.ActorOrchestrator, event.ActorTask, event.ActorNarrator} {
		pick, ok := after[actor]
		if !ok || pick == before[actor] || pick.Provider == "" || pick.Model == "" {
			continue
		}
		a, err := config.PickRoute(r.opts.Project, r.configActor(actor), pick.Provider, pick.Model, pick.Effort)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", actor, err)
		}
		// The picked route itself must be usable (its key set); fallbacks
		// without a key are skipped as usual.
		if _, err := a.Endpoint(); err != nil {
			return nil, fmt.Errorf("%s: %w", actor, err)
		}
		eps, err := a.Routes()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", actor, err)
		}
		out = append(out, routeSwap{actor: actor, pick: pick, eps: eps})
	}
	return out, nil
}

// configActor is the configured actor for a role.
func (r *Runtime) configActor(actor string) config.Actor {
	switch actor {
	case event.ActorTask:
		return r.cfg.Task
	case event.ActorNarrator:
		return r.cfg.Narrator
	default:
		return r.cfg.Orchestrator
	}
}

// swapRoute installs a resolved route between turns (the control poll runs
// on the loop goroutine) and records it; the Route event also refreshes the
// phone's model chip.
func (r *Runtime) swapRoute(sw routeSwap) {
	nc := r.newClient(sw.eps[0], sw.actor)
	switch sw.actor {
	case event.ActorTask:
		r.taskRoutes, r.taskClient = sw.eps, nc
	case event.ActorNarrator:
		r.narrRoutes, r.narrClient = sw.eps, nc
	default:
		r.orchRoutes, r.orchClient = sw.eps, nc
	}
	r.append(event.New(event.Route, event.ActorHarness, event.RouteData{
		Actor: sw.actor, Provider: sw.eps[0].Protocol, BaseURL: sw.eps[0].BaseURL, Model: sw.eps[0].Model,
		Reason: fmt.Sprintf("runtime settings: provider %s, model %s, effort %s", sw.pick.Provider, sw.pick.Model, sw.pick.Effort),
	}))
	r.ui.Log("%s: runtime route -> %s %s @ %s", sw.actor, sw.pick.Provider, sw.eps[0].Model, sw.pick.Effort)
}
