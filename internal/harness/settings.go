package harness

import (
	"fmt"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
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
	r.applyRoutes(values.Routes)
	return nil
}

// applyRoutes swaps actor model routes chosen through the session settings.
// It runs on the loop goroutine (the control poll is), so clients are swapped
// directly between turns; a Route event records the switch, which also
// refreshes the phone's thread meta chips.
func (r *Runtime) applyRoutes(routes map[string]runtimecontrol.Route) {
	for actor, rt := range routes {
		if rt.Provider == "" || rt.Model == "" {
			continue
		}
		cur := r.clientFor(actor).Endpoint
		curProvider := ""
		if pr, ok := config.ProviderForURL(cur.BaseURL); ok {
			curProvider = pr.ID
		}
		if curProvider == rt.Provider && cur.Model == rt.Model && cur.ReasoningEffort == rt.Effort {
			continue // unchanged
		}
		m, pr, ok := config.ResolveLiveModel(r.opts.Project, rt.Provider, rt.Model)
		if !ok {
			r.ui.Log("runtime settings: %s: model %q is not listed by provider %q; keeping %s", actor, rt.Model, rt.Provider, cur.Model)
			continue
		}
		a := config.Actor{
			Protocol:        m.ProtocolFor(),
			BaseURL:         pr.BaseURL,
			Model:           m.ID,
			APIKeyEnv:       pr.KeyEnv,
			ReasoningEffort: rt.Effort,
		}
		if m.Context > 0 {
			a.ContextTokens = m.Context
		}
		eps, err := a.Routes()
		if err != nil {
			r.ui.Log("runtime settings: %s route %s/%s is not routable: %v", actor, rt.Provider, rt.Model, err)
			continue
		}
		nc := r.newClient(eps[0], actor)
		switch actor {
		case event.ActorTask:
			r.taskRoutes, r.taskClient = eps, nc
		case event.ActorNarrator:
			r.narrRoutes, r.narrClient = eps, nc
		default:
			r.orchRoutes, r.orchClient = eps, nc
		}
		r.append(event.New(event.Route, event.ActorHarness, event.RouteData{
			Actor: actor, Provider: eps[0].Protocol, BaseURL: eps[0].BaseURL, Model: eps[0].Model,
			Reason: fmt.Sprintf("runtime settings: provider %s, model %s, effort %s", rt.Provider, rt.Model, rt.Effort),
		}))
		r.ui.Log("%s: runtime route -> %s %s @ %s", actor, pr.Label, m.ID, rt.Effort)
	}
}
