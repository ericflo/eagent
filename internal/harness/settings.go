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
	c.Stop = r.acceptRemoteStop
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
		r.syncRelaySettings()
		if err := r.runtimeControl.Poll(); err != nil {
			r.ui.Log("live settings: %v", err)
		}
		// A stop from the app ends the session the way the web UI's Stop
		// does, now that the controller has acknowledged it.
		if r.remoteStop {
			r.stopSession("the user stopped the session")
		}
	}
}

// acceptRemoteStop records a session.stop the app sent (through the
// connector) and arranges the shutdown for after this poll. Loop goroutine.
func (r *Runtime) acceptRemoteStop(q runtimecontrol.Request) error {
	if r.ending || r.ctx.Err() != nil || r.runtimeControl == nil || q.Proposal.Generation != r.runtimeControl.Generation {
		return fmt.Errorf("the session is already ending")
	}
	previousSeq := r.st.LastSeq()
	ev := r.append(event.New("session.stop_requested", event.ActorHarness, map[string]any{
		"command_id": q.ID, "user_id": q.UserID, "proposal_sha256": q.Digest, "generation": q.Proposal.Generation, "resource_key": q.Grant.Key,
	}))
	if ev.Seq <= previousSeq {
		return fmt.Errorf("the stop was not durably recorded")
	}
	r.ui.Log("stop requested from FinaleChat")
	r.remoteStop = true
	return nil
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
	// So is a relay pick its agent cannot take.
	repicks := relayRepicks(r.activeSettings.Relays, values.Relays)
	for name, pick := range repicks {
		rel := r.st.Relays[name]
		if rel == nil || !rel.Active() {
			return fmt.Errorf("relay %s is no longer running", name)
		}
		if err := r.checkRelayPick(rel.Profile, pick); err != nil {
			return err
		}
	}
	previousSeq := r.st.LastSeq()
	ev := r.append(event.New(runtimecontrol.EventType, event.ActorHarness, map[string]any{
		"format": runtimecontrol.Schema, "command_id": q.ID, "user_id": q.UserID, "proposal_sha256": q.Digest,
		"generation": q.Proposal.Generation, "resource_key": q.Grant.Key, "previous": r.activeSettings, "values": values, "result": result,
	}))
	if ev.Seq <= previousSeq || ev.Seq != r.st.LastSeq() {
		return fmt.Errorf("the settings event was not durably recorded")
	}
	changedTimer := r.activeSettings.TickSeconds != values.TickSeconds
	r.activeSettings = values
	if changedTimer {
		r.armTicker()
	}
	for _, sw := range swaps {
		r.swapRoute(sw)
	}
	for name, pick := range repicks {
		msg, err := r.repickRelay(name, pick)
		if err != nil {
			r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "relay", Text: err.Error()}))
			continue
		}
		r.ui.Log("relay %s: %s", name, msg)
		// Said in the conversation too, as /model says it: a change made in
		// the app's settings is otherwise silent until the agent restarts.
		// This runs on the loop, so the message is recorded here, in order.
		r.recordMessage(event.MessageData{Text: msg, Auto: true})
	}
	return nil
}

// routeSwap is one model's new route list, resolved from a settings pick.
type routeSwap struct {
	who  string
	pick runtimecontrol.Route
	eps  []llm.Endpoint
	conf config.ModelRoute // the route as configured: window and reply ceiling
}

// routeChanges resolves the models whose pick changed. One whose pick did
// not change keeps its routes, including a fallback it switched to. A
// changed pick starts from the configured route (see config.PickRoute:
// limits stay, another model drops the old model's fallback chain).
func (r *Runtime) routeChanges(before, after map[string]runtimecontrol.Route) ([]routeSwap, error) {
	var out []routeSwap
	for _, who := range []string{runtimecontrol.RouteEagent, runtimecontrol.RouteSummarizer} {
		pick, ok := after[who]
		if !ok || pick == before[who] || pick.Provider == "" || pick.Model == "" || r.models[who] == nil {
			continue
		}
		a, err := config.PickRoute(r.opts.Project, r.configRoute(who), pick.Provider, pick.Model, pick.Effort)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", who, err)
		}
		// The picked route itself must be usable (its key set); fallbacks
		// without a key are skipped as usual.
		if _, err := a.Endpoint(); err != nil {
			return nil, fmt.Errorf("%s: %w", who, err)
		}
		eps, err := a.Routes()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", who, err)
		}
		out = append(out, routeSwap{who: who, pick: pick, eps: eps, conf: a})
	}
	return out, nil
}

// configRoute is the configured route of one of the session's models.
func (r *Runtime) configRoute(who string) config.ModelRoute {
	if who == runtimecontrol.RouteSummarizer {
		return r.cfg.Summarizer.Route(r.cfg.Model)
	}
	return r.cfg.Model
}

// swapRoute installs a resolved route between calls (the control poll runs
// on the loop goroutine; a call in flight finishes on the route it started
// with) and records it; the Route event also refreshes the phone's model
// chip. A new model has no cache of the conversation, so its first call
// pays for the whole prompt once.
func (r *Runtime) swapRoute(sw routeSwap) {
	r.flushSilent() // the looks so far were made on the old route
	r.models[sw.who] = &modelRoute{routes: sw.eps, client: r.newClient(sw.eps[0], sw.who), conf: sw.conf}
	r.append(event.New(event.Route, event.ActorHarness, event.RouteData{
		Actor: sw.who, Provider: sw.eps[0].Protocol, BaseURL: sw.eps[0].BaseURL, Model: sw.eps[0].Model,
		Reason: fmt.Sprintf("runtime settings: provider %s, model %s, effort %s", sw.pick.Provider, sw.pick.Model, sw.pick.Effort),
	}))
	r.ui.Log("%s: runtime route -> %s %s @ %s", sw.who, sw.pick.Provider, sw.eps[0].Model, sw.pick.Effort)
}
