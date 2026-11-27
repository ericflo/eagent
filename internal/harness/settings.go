package harness

import (
	"fmt"
	"strings"
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
	c.Routes = r.runningRoutes
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
	// What is refused before anything is recorded is a refusal: the app is
	// told the change was rejected, and why (runtimecontrol.Refusal).
	refuse := func(err error) error { return runtimecontrol.Refusal{Err: err} }
	if r.ending || r.ctx.Err() != nil || !q.Expires.After(time.Now()) || r.runtimeControl == nil || q.Proposal.Generation != r.runtimeControl.Generation {
		return refuse(fmt.Errorf("the targeted runtime stopped before applying the change"))
	}
	// Resolve every changed route before recording anything, so a route
	// that cannot run fails the change instead of being reported as applied.
	swaps, err := r.routeChanges(r.activeSettings.Routes, values.Routes)
	if err != nil {
		return refuse(err)
	}
	// So is a relay pick its agent cannot take.
	repicks := relayRepicks(r.activeSettings.Relays, values.Relays)
	for name, pick := range repicks {
		rel := r.st.Relays[name]
		if rel == nil || !rel.Active() {
			return refuse(fmt.Errorf("relay %s is no longer running", name))
		}
		if err := r.beingReplaced(rel); err != nil {
			return refuse(err)
		}
		if err := r.checkRelayPick(rel.Profile, pick); err != nil {
			return refuse(err)
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
		if !ok || pick == before[who] || pick.Model == "" || r.models[who] == nil {
			continue
		}
		a, err := r.resolvePick(who, pick)
		if err != nil {
			return nil, err
		}
		// The pick names the effort the session reports (ApplyValues
		// resolves it), "" (send none) included, so it is the one sent,
		// not the configured route's.
		a.ReasoningEffort = pick.Effort
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

// pickBase is the configured route a pick of a model the session already
// has starts from: the entry the session is on when the pick names that
// one (an effort change on a fallback keeps the fallback's limits and key,
// even when two entries name one model), else the entry of the configured
// chain the pick names (the first route picked again is that route, with
// its fallbacks), else the one of the routes a settings pick put in place.
// A pick of another model has none: it starts from the configured route.
func (r *Runtime) pickBase(who string, pick runtimecontrol.Route) (config.ModelRoute, bool) {
	project := r.opts.Project
	m := r.models[who]
	if cur := m.confAt(); cur != nil && runtimecontrol.Names(project, *cur, pick) {
		return *cur, true
	}
	for _, chain := range []config.ModelRoute{r.configRoute(who), m.conf} {
		if entry, ok := runtimecontrol.ChainEntry(project, chain, pick); ok {
			return entry, true
		}
	}
	return config.ModelRoute{}, false
}

// resolvePick is the configured route a changed pick of who's model
// starts from: the entry of a chain the session has that the pick names
// (pickBase), else the configured route picked from the catalog.
func (r *Runtime) resolvePick(who string, pick runtimecontrol.Route) (config.ModelRoute, error) {
	var a config.ModelRoute
	base, known := r.pickBase(who, pick)
	switch {
	case known && pick.Provider == "":
		// An address no provider knows has no catalog to pick from:
		// the entry as configured, at the effort asked for.
		if pick.Effort != "" && config.EffortIndex(pick.Effort) == len(config.EffortOrder) {
			return a, fmt.Errorf("%s: effort %q is not a known level (%s)", who, pick.Effort, strings.Join(config.EffortOrder, ", "))
		}
		a = base
	case pick.Provider == "":
		return a, fmt.Errorf("%s: model %q is not at an address eagent knows a provider for, and this session's routes do not have it; pick a provider", who, pick.Model)
	default:
		if !known {
			base = r.configRoute(who)
		}
		var err error
		if a, err = config.PickRoute(r.opts.Project, base, pick.Provider, pick.Model, pick.Effort); err != nil {
			return a, fmt.Errorf("%s: %w", who, err)
		}
	}
	return a, nil
}

// runningRoutes is Controller.Routes: for each of the session's models
// that routes names, the chain it runs as configured (from the entry it is
// on, so a session on a fallback is saved as that fallback and those
// after it), or, for a pick this change makes, the one it will run once
// the change is applied. Loop goroutine (the controller's poll).
func (r *Runtime) runningRoutes(routes map[string]runtimecontrol.Route) (map[string]config.ModelRoute, error) {
	out := map[string]config.ModelRoute{}
	for _, who := range []string{runtimecontrol.RouteEagent, runtimecontrol.RouteSummarizer} {
		pick, ok := routes[who]
		m := r.models[who]
		if !ok || m == nil || pick.Model == "" {
			continue
		}
		if pick == r.activeSettings.Routes[who] {
			if cur := m.confAt(); cur != nil {
				out[who] = *cur
			} else {
				out[who] = m.conf
			}
			continue
		}
		a, err := r.resolvePick(who, pick)
		if err != nil {
			return nil, err
		}
		a.ReasoningEffort = pick.Effort
		out[who] = a
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
	moved := true
	old := r.models[sw.who]
	if old != nil && old.client != nil {
		moved = !sameRoute(old.client.Endpoint, sw.eps[0])
	}
	r.models[sw.who] = &modelRoute{routes: sw.eps, client: r.newClient(sw.eps[0], sw.who), conf: sw.conf}
	if !moved {
		r.models[sw.who].out = old.out // the same model at the same address: an outage goes on
	}
	if sw.who == event.ActorEagent && moved {
		// The new one starts clean (newModel) and is tried at the loop's
		// next tick (the poll that applied the change is followed by one).
		r.newModel()
	}
	r.recordRoute(event.RouteData{
		Actor: sw.who, Provider: sw.eps[0].Protocol, BaseURL: sw.eps[0].BaseURL, Model: sw.eps[0].Model,
		Reason: fmt.Sprintf("runtime settings: provider %s, model %s, effort %s", sw.pick.Provider, sw.pick.Model, sw.pick.Effort),
	})
	r.ui.Log("%s: runtime route -> %s %s @ %s", sw.who, sw.pick.Provider, sw.eps[0].Model, sw.pick.Effort)
}

// newModel forgets, when eagent's calls move to another model or address
// (a pick in settings, swapRoute; a fallback, takeRoute), what the old model
// did, which belongs to it: its failures and the wait before the next try
// (up to an hour), a refusal of the prompt as too long (which would write
// the next dossier from the files), and how its provider counts tokens
// (recordRoute forgets the first call's size). The restarts in a row it
// needed are forgotten by the move on the record (restartOf). If the new
// one fails too, it backs off on its own count. Loop goroutine.
func (r *Runtime) newModel() {
	t := &r.turns
	t.failures, t.retryAt, t.failTold = 0, time.Time{}, false
	t.overflowed, t.futileLogged = false, false
	r.tokenScale = 0
}
