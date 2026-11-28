package harness

import (
	"fmt"
	"maps"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/state"
)

// A relay's model and reasoning effort can change while it runs: from the
// phone's thread settings (the session's live settings) or when the user
// asks eagent. The agent starts again on its own conversation with the new
// flags; while it is in a turn or starting, or a send to it is still on its
// way, the change waits for it to be between turns, so no work is cut off.

// relayDefaults is what a relay runs when the session picks nothing: its
// profile's model and effort, else the agent's own configured defaults.
func (r *Runtime) relayDefaults(rel *state.Relay) (model, effort string) {
	p := r.cfg.Relay.RelayProfiles()[rel.Profile]
	return relay.ProfileDefaults(rel.Agent, p.Model, p.Effort)
}

// waitingPick is the change of model or effort relay name starts with
// next, if one waits (applyWaitingRepick applies it). A stopped relay has
// none: a later start under its name runs what it is asked to. Loop
// goroutine.
func (r *Runtime) waitingPick(name string) (relayPick, bool) {
	pick, ok := r.relayNext[name]
	if rel := r.st.Relays[name]; !ok || rel == nil || rel.Ended {
		return relayPick{}, false
	}
	return pick, true
}

// setWaitingPick makes pick the change relay name starts with next. Loop
// goroutine.
func (r *Runtime) setWaitingPick(name string, pick relayPick) {
	if r.relayNext == nil {
		r.relayNext = map[string]relayPick{}
	}
	r.relayNext[name] = pick
}

// runsNext is what relay rel starts with next: a change that waits, else
// the picks it runs now.
func (r *Runtime) runsNext(rel *state.Relay) relayPick {
	if pick, ok := r.waitingPick(rel.Name); ok {
		return pick
	}
	return relayPick{rel.Model, rel.Effort}
}

// relayRoutes are the session's relays as live settings: each one's picks
// (a change still waiting included) and what leaving them alone gives.
func (r *Runtime) relayRoutes() map[string]runtimecontrol.RelayRoute {
	out := map[string]runtimecontrol.RelayRoute{}
	for _, rel := range r.st.ActiveRelays() {
		_, waiting := r.waitingPick(rel.Name)
		pick := r.runsNext(rel)
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

// repickRelay gives a running relay another model or effort: now, unless
// the record shows the agent in a turn or starting, still waits on a send
// to it (one on its way, or held for the end of its turn, starts the
// agent's next turn), or shows it idle with something new of it moments
// ago (repickGrace); then when it is next between turns
// (applyWaitingRepick). It says which. Loop goroutine.
func (r *Runtime) repickRelay(name string, pick relayPick) (string, error) {
	rel := r.st.Relays[name]
	if rel == nil || !rel.Active() {
		return "", fmt.Errorf("no relay %q is running", name)
	}
	if err := r.checkRelayPick(rel.Profile, pick); err != nil {
		return "", err
	}
	if err := r.beingReplaced(rel); err != nil {
		return "", err
	}
	// A start of this relay under way: what it runs next is what that
	// start launches, not what the record says it runs now.
	job := r.relays.launch(name)
	runs := relayPick{rel.Model, rel.Effort}
	if job != nil {
		runs = job.pick
	}
	then := " it starts again with " + r.pickText(rel, pick)
	switch {
	case pick == runs:
		delete(r.relayNext, name) // a change that waited is taken back
		r.syncRelaySettings()
		r.publishRelayMeta()
		if job != nil {
			return rel.Who() + " is already starting again with " + r.pickText(rel, pick) + ".", nil
		}
		return rel.Who() + " already runs that.", nil
	case job != nil || rel.State == "working" || rel.State == "starting" || rel.Awaited() > 0 || rel.State == "idle" && time.Since(rel.LastEventAt) < repickGrace:
		r.setWaitingPick(name, pick)
		r.syncRelaySettings()
		r.publishRelayMeta() // the thread header says a change is waiting
		switch {
		case job != nil:
			return rel.Who() + " is starting again; it changes to " + r.pickText(rel, pick) + " once it is up and between turns.", nil
		case rel.State == "working":
			return rel.Who() + " is in a turn;" + then + " when the turn ends, on the same conversation.", nil
		case rel.State == "starting":
			return rel.Who() + " is still starting;" + then + " once it is ready and between turns, on the same conversation.", nil
		case rel.Awaited() > 0:
			return rel.Who() + " has a message on its way;" + then + " when the turn that message starts ends, on the same conversation.", nil
		}
		r.applyWaitingRepick(name) // arms the grace's timer
		return rel.Who() + " was active a moment ago;" + then + " in a few seconds, once it is plainly between turns, on the same conversation.", nil
	}
	if err := r.restartRelayWith(rel, pick); err != nil {
		return "", err
	}
	return rel.Who() + " is starting again with " + r.pickText(rel, pick) + ", continuing its own conversation.", nil
}

// beingReplaced refuses a change for a relay that a switch under way hands
// over from: the start is another agent's, rel does not start again, so
// nothing is kept for it. Loop goroutine.
func (r *Runtime) beingReplaced(rel *state.Relay) error {
	if job := r.relays.launch(rel.Name); job != nil && job.name != rel.Name {
		return fmt.Errorf("%s is being replaced by %s; change the model once that is up", rel.Who(), relay.AgentLabel(job.spec.Profile.Agent))
	}
	return nil
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
// The pick stays the waiting change until then (finishLaunch). What is
// sent to the relay meanwhile waits and is typed into the new agent.
func (r *Runtime) restartRelayWith(rel *state.Relay, pick relayPick) error {
	job, err := r.prepareLaunch(rel.Profile, rel.Dir, rel.Name, true, pick)
	if err != nil {
		delete(r.relayNext, rel.Name)
		r.syncRelaySettings()
		r.publishRelayMeta()
		return err
	}
	r.setWaitingPick(rel.Name, pick)
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

// repickGrace is how long the record must have had nothing new of an agent
// before a change that waited is applied. What the agent does next shows
// within moments: a message it took into its own queue while it worked
// (Claude Code does, and the record has it arrived) starts the next turn
// right after the turn's end, and one of its own commands just typed
// (/new, /clear) moves it to another session. A restart in those moments
// would kill the agent with the message in hand, or open the session the
// command left. Tests zero it.
var repickGrace = 5 * time.Second

// applyWaitingRepick starts a relay again with a change that waited, once
// the record shows it between turns with no send to it still on its way,
// and nothing new of it for repickGrace. Looked at after each thing the
// record takes of the relay that can end the wait: a state, a turn's end,
// a send that did not go or a command typed. Loop goroutine.
func (r *Runtime) applyWaitingRepick(name string) {
	pick, ok := r.relayNext[name]
	rel := r.st.Relays[name]
	if !ok || rel == nil {
		return
	}
	if !rel.Active() {
		delete(r.relayNext, name)
		r.syncRelaySettings()
		return
	}
	if r.ending || r.relays.launch(name) != nil || rel.State != "idle" || rel.Awaited() > 0 {
		return // (no agent is started once the session is ending)
	}
	if wait := repickGrace - time.Since(rel.LastEventAt); wait > 0 {
		time.AfterFunc(wait, func() { r.post(func() { r.applyWaitingRepick(name) }) })
		return
	}
	if err := r.restartRelayWith(rel, pick); err != nil {
		r.repickFailed(rel, pick, err)
	}
}

// repickFailed says that a change that waited could not start the agent
// again: the change is dropped. Loop goroutine.
func (r *Runtime) repickFailed(rel *state.Relay, pick relayPick, err error) {
	r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "relay", Text: "could not start " + rel.Who() + " again with " + r.pickText(rel, pick) + ": " + err.Error()}))
	r.ui.Log("relay %s: %v", rel.Name, err)
}
