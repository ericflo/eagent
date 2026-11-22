package harness

import (
	"fmt"
	"maps"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/state"
)

// A relay's model and reasoning effort can change while it runs: from the
// phone's thread settings (the session's live settings) or when the user
// asks eagent. The agent starts again on its own conversation with the new
// flags; while it is in a turn, the change waits for that turn to end so no
// work is cut off.

// relayDefaults is what a relay runs when the session picks nothing: its
// profile's model and effort, else the agent's own configured defaults.
func (r *Runtime) relayDefaults(rel *state.Relay) (model, effort string) {
	p := r.cfg.Relay.RelayProfiles()[rel.Profile]
	return relay.ProfileDefaults(rel.Agent, p.Model, p.Effort)
}

// relayRoutes are the session's relays as live settings: each one's picks
// (a change still waiting included) and what leaving them alone gives.
func (r *Runtime) relayRoutes() map[string]runtimecontrol.RelayRoute {
	out := map[string]runtimecontrol.RelayRoute{}
	for _, rel := range r.st.ActiveRelays() {
		pick, waiting := r.relayRepick[rel.Name]
		if !waiting {
			pick = relayPick{rel.Model, rel.Effort}
		}
		model, effort := r.relayDefaults(rel)
		out[rel.Name] = runtimecontrol.RelayRoute{Agent: rel.Agent, Model: pick.Model, Effort: pick.Effort, DefaultModel: model, DefaultEffort: effort, Waiting: waiting}
	}
	return out
}

// syncRelaySettings brings the live settings' relays up to date with the
// session's (one started, stopped, restarted, or a change applied). Loop
// goroutine.
func (r *Runtime) syncRelaySettings() {
	relays := r.relayRoutes()
	if len(relays) == 0 {
		relays = nil
	}
	// The controller's copy too: an applied change replaces it after the
	// harness has already marked a relay as waiting.
	if maps.Equal(relays, r.activeSettings.Relays) && (r.runtimeControl == nil || maps.Equal(relays, r.runtimeControl.Values.Relays)) {
		return
	}
	r.activeSettings.Relays = relays
	if r.runtimeControl != nil {
		if err := r.runtimeControl.Update(r.activeSettings); err != nil {
			r.ui.Log("live settings: %v", err)
		}
	}
}

// relayRepicks are the relays whose picks a settings change alters.
func relayRepicks(before, after map[string]runtimecontrol.RelayRoute) map[string]relayPick {
	out := map[string]relayPick{}
	for name, rt := range after {
		if old, ok := before[name]; ok && (old.Model != rt.Model || old.Effort != rt.Effort) {
			out[name] = relayPick{rt.Model, rt.Effort}
		}
	}
	return out
}

// repickRelay gives a running relay another model or effort: now when the
// agent is between turns, else when its current turn ends. It says which.
// Loop goroutine.
func (r *Runtime) repickRelay(name string, pick relayPick) (string, error) {
	rel := r.st.Relays[name]
	if rel == nil || !rel.Active() {
		return "", fmt.Errorf("no relay %q is running", name)
	}
	if err := r.checkRelayPick(rel.Profile, pick); err != nil {
		return "", err
	}
	if pick == (relayPick{rel.Model, rel.Effort}) {
		delete(r.relayRepick, name)
		r.syncRelaySettings()
		return rel.Who() + " already runs that.", nil
	}
	if rel.State == "working" {
		if r.relayRepick == nil {
			r.relayRepick = map[string]relayPick{}
		}
		r.relayRepick[name] = pick
		r.syncRelaySettings()
		return rel.Who() + " is in a turn; it starts again with " + r.pickText(rel, pick) + " when the turn ends, on the same conversation.", nil
	}
	if _, err := r.restartRelayWith(rel, pick); err != nil {
		return "", err
	}
	return rel.Who() + " started again with " + r.pickText(rel, pick) + ", continuing its own conversation.", nil
}

// pickText says what a relay runs with a pick: "sonnet at high".
func (r *Runtime) pickText(rel *state.Relay, pick relayPick) string {
	model, effort := r.relayDefaults(rel)
	model, effort = firstNonEmpty(pick.Model, model), firstNonEmpty(pick.Effort, effort)
	switch {
	case model != "" && effort != "":
		return model + " at " + effort
	case model != "":
		return model
	case effort != "":
		return "its default model at " + effort
	}
	return "its own defaults"
}

// restartRelayWith ends a relay's agent and starts it again on its own
// conversation with the picks. Loop goroutine.
func (r *Runtime) restartRelayWith(rel *state.Relay, pick relayPick) (string, error) {
	delete(r.relayRepick, rel.Name)
	if seat := r.relays.get(rel.Name); seat != nil {
		_ = seat.Kill()
		r.relays.drop(rel.Name, seat)
	}
	started, err := r.startRelay(rel.Profile, rel.Dir, rel.Name, true, pick)
	r.syncRelaySettings()
	return started, err
}

// applyWaitingRepick starts a relay again with a change that waited for its
// turn to end. Loop goroutine.
func (r *Runtime) applyWaitingRepick(name string) {
	pick, ok := r.relayRepick[name]
	rel := r.st.Relays[name]
	if !ok || rel == nil {
		return
	}
	if !rel.Active() {
		delete(r.relayRepick, name)
		r.syncRelaySettings()
		return
	}
	if _, err := r.restartRelayWith(rel, pick); err != nil {
		r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "relay", Text: "could not start " + rel.Who() + " again with " + r.pickText(rel, pick) + ": " + err.Error()}))
		r.ui.Log("relay %s: %v", name, err)
	}
}
