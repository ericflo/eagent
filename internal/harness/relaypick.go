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
// flags; while it is in a turn, or its terminal holds it blocked, the change
// waits for that to end so no work is cut off and no dialog killed.

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
	if job := r.relays.launch(name); job != nil && job.name == name {
		// A start of this relay is under way: what it runs next is what
		// that start launches, not what the record says it runs now.
		if r.relayRepick == nil {
			r.relayRepick = map[string]relayPick{}
		}
		if pick == job.pick {
			// Nothing waits beyond the start (finishLaunch clears a pick
			// equal to the one it started); one waiting already is dropped.
			if _, waiting := r.relayRepick[name]; waiting {
				r.relayRepick[name] = pick
			}
			r.syncRelaySettings()
			r.publishRelayMeta()
			return rel.Who() + " is already starting again with " + r.pickText(rel, pick) + ".", nil
		}
		// Applied once the new agent is idle (applyWaitingRepick).
		r.relayRepick[name] = pick
		r.syncRelaySettings()
		r.publishRelayMeta()
		return rel.Who() + " is starting again; it changes to " + r.pickText(rel, pick) + " once it is up.", nil
	}
	if job := r.relays.launch(name); job != nil {
		// The start under way is another agent's, which this relay hands
		// over to: it does not start again, so nothing is kept for it.
		return "", fmt.Errorf("%s is being replaced by %s; change the model once that is up", rel.Who(), relay.AgentLabel(job.spec.Profile.Agent))
	}
	if pick == (relayPick{rel.Model, rel.Effort}) {
		delete(r.relayRepick, name)
		r.syncRelaySettings()
		r.publishRelayMeta()
		return rel.Who() + " already runs that.", nil
	}
	if held := r.seatBlocked(name); rel.State == "working" || held {
		if r.relayRepick == nil {
			r.relayRepick = map[string]relayPick{}
		}
		r.relayRepick[name] = pick
		r.syncRelaySettings()
		r.publishRelayMeta() // the thread header says a change is waiting
		if held {
			return rel.Who() + " is blocked (something on its screen waits for an answer, or it has gone quiet in a turn); it starts again with " + r.pickText(rel, pick) + " once that is over and it is between turns, on the same conversation.", nil
		}
		return rel.Who() + " is in a turn; it starts again with " + r.pickText(rel, pick) + " when the turn ends, on the same conversation.", nil
	}
	if err := r.restartRelayWith(rel, pick); err != nil {
		return "", err
	}
	return rel.Who() + " is starting again with " + r.pickText(rel, pick) + ", continuing its own conversation.", nil
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
// conversation with the picks. Loop goroutine; the stop and the launch run
// off it, and how the start went is recorded when it is done: started, or
// exited with the reason (and an error notice) when it could not start.
// The pick stays the waiting change until then. What is sent to the relay
// meanwhile waits and is typed into the new agent.
func (r *Runtime) restartRelayWith(rel *state.Relay, pick relayPick) error {
	job, err := r.prepareLaunch(rel.Profile, rel.Dir, rel.Name, true, pick)
	if err != nil {
		delete(r.relayRepick, rel.Name)
		r.syncRelaySettings()
		r.publishRelayMeta()
		return err
	}
	if r.relayRepick == nil {
		r.relayRepick = map[string]relayPick{}
	}
	r.relayRepick[rel.Name] = pick
	r.beginLaunch(job, rel.Name)
	who, text := rel.Who(), r.pickText(rel, pick)
	go func() {
		job.run()
		ran := false
		r.sync(func() {
			ran = true
			if _, err := r.finishLaunch(job); err != nil {
				r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "relay", Text: "could not start " + who + " again with " + text + ": " + err.Error()}))
				r.ui.Log("relay %s: %v", job.name, err)
			}
		})
		if !ran {
			r.abandonLaunch(job) // the session ended meanwhile: nothing would watch it
		}
	}()
	return nil
}

// seatBlocked says whether the relay's terminal holds its agent blocked
// now: on a dialog (pi's holds it through a turn end it writes under one,
// which the record takes as idle), quiet in a turn, or a start not yet
// ready. No change starts it again then: the record's state alone cannot
// say so. Loop goroutine.
func (r *Runtime) seatBlocked(name string) bool {
	seat := r.relays.get(name)
	return seat != nil && seat.Snapshot().State == "blocked"
}

// applyWaitingRepick starts a relay again with a change that waited for its
// turn to end, unless its seat holds it blocked. Loop goroutine.
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
	if r.relays.launch(name) != nil {
		return // a start under way applies it when it is up
	}
	if r.seatBlocked(name) {
		return // applied when its seat says it is idle
	}
	if err := r.restartRelayWith(rel, pick); err != nil {
		r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "relay", Text: "could not start " + rel.Who() + " again with " + r.pickText(rel, pick) + ": " + err.Error()}))
		r.ui.Log("relay %s: %v", name, err)
	}
}
