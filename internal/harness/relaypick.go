package harness

import (
	"errors"
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

// nextPick (the entry) is a change of model or effort a relay starts with
// next: one that waits (for its turn to end, a dialog, a start under way),
// or, once its agent exited, one kept for its next start (kept).
type nextPick struct {
	pick relayPick
	kept bool
}

// nextPick says the change relay name starts with next, if any, and whether
// it waits (else it is kept for its next start). A stopped relay has none:
// a later start under its name runs what it is asked to. The one reader of
// relayNext. Loop goroutine.
func (r *Runtime) nextPick(name string) (pick relayPick, ok, waiting bool) {
	np, ok := r.relayNext[name]
	if rel := r.st.Relays[name]; !ok || rel == nil || rel.Ended {
		return relayPick{}, false, false
	}
	return np.pick, true, !np.kept
}

// setNextPick sets the change relay name starts with next, waiting or kept
// (keep), or clears it (pick nil). The one writer of relayNext; callers
// bring the live settings and the thread header up to date. Loop goroutine.
func (r *Runtime) setNextPick(name string, pick *relayPick, keep bool) {
	if pick == nil {
		delete(r.relayNext, name)
		return
	}
	if r.relayNext == nil {
		r.relayNext = map[string]nextPick{}
	}
	r.relayNext[name] = nextPick{*pick, keep}
}

// runsNext is what relay rel starts with next: the change it starts with
// (nextPick), else the picks it runs now.
func (r *Runtime) runsNext(rel *state.Relay) relayPick {
	if pick, ok, _ := r.nextPick(rel.Name); ok {
		return pick
	}
	return relayPick{rel.Model, rel.Effort}
}

// relayRoutes are the session's relays as live settings: each one's picks
// (a change still waiting, or kept for its next start, included) and what
// leaving them alone gives.
func (r *Runtime) relayRoutes() map[string]runtimecontrol.RelayRoute {
	out := map[string]runtimecontrol.RelayRoute{}
	for _, rel := range r.st.ActiveRelays() {
		_, _, waiting := r.nextPick(rel.Name)
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
		if pick == job.pick {
			// Nothing waits beyond the start (finishLaunch clears a pick
			// equal to the one it started); one waiting already is dropped.
			if _, ok, _ := r.nextPick(name); ok {
				r.setNextPick(name, &pick, false)
			}
			r.syncRelaySettings()
			r.publishRelayMeta()
			return rel.Who() + " is already starting again with " + r.pickText(rel, pick) + ".", nil
		}
		// Applied once the new agent is idle (applyWaitingRepick).
		r.setNextPick(name, &pick, false)
		r.syncRelaySettings()
		r.publishRelayMeta()
		return rel.Who() + " is starting again; it changes to " + r.pickText(rel, pick) + " once it is up.", nil
	}
	if err := r.beingReplaced(rel); err != nil {
		return "", err
	}
	if pick == (relayPick{rel.Model, rel.Effort}) {
		// What it runs: a change it would start with next (waiting, or kept
		// since its agent exited) is taken back.
		r.setNextPick(name, nil, false)
		r.syncRelaySettings()
		r.publishRelayMeta()
		return rel.Who() + " already runs that.", nil
	}
	if held := r.heldBack(rel); held != "" {
		r.setNextPick(name, &pick, false)
		r.syncRelaySettings()
		r.publishRelayMeta() // the thread header says a change is waiting
		then := " it starts again with " + r.pickText(rel, pick)
		switch why := r.busyWith(rel); {
		case held == "dialog":
			return rel.Who() + " is waiting on a dialog on its screen (see relay_screen);" + then + " once that is answered and it is between turns, on the same conversation.", nil
		case why == "starting":
			return rel.Who() + " is still starting;" + then + " once it is ready, on the same conversation.", nil
		case why == "working":
			return rel.Who() + " is in a turn;" + then + " when the turn ends, on the same conversation.", nil
		case why == "message":
			return rel.Who() + " has a message on its way that it reads next;" + then + " when the turn that message starts ends, on the same conversation.", nil
		case why == "command":
			return rel.Who() + " is acting on one of its own commands;" + then + " once it has, on the same conversation.", nil
		}
		return rel.Who() + " is not between turns yet;" + then + " once nothing waits on it, on the same conversation.", nil
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
// The pick stays the waiting change until then. What is sent to the relay
// meanwhile waits and is typed into the new agent, after held (what waited
// for the start before, finishLaunch).
func (r *Runtime) restartRelayWith(rel *state.Relay, pick relayPick, held ...heldSend) error {
	job, err := r.prepareLaunch(rel.Profile, rel.Dir, rel.Name, true, pick)
	if err != nil {
		r.setNextPick(rel.Name, nil, false)
		r.syncRelaySettings()
		r.publishRelayMeta()
		return err
	}
	r.setNextPick(rel.Name, &pick, false)
	r.beginLaunch(job, rel.Name)
	if len(held) > 0 {
		r.relays.mu.Lock()
		job.held = append(held, job.held...)
		r.relays.mu.Unlock()
	}
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

// heldBack says what holds a model change back from starting the agent
// again now, "" when nothing does, as the agent's seat says (relay.Status):
// "dialog" while a dialog the agent reports holds it, "busy" while the seat
// is not quiet (in a turn, a send on its way or being typed, one of the
// agent's own commands not yet acted on as far as the seat can tell, a move
// not yet followed, a catch-up, a block the seat does not know to be
// stuck). The record cannot say so: it infers from what it was told, and
// what it was told runs behind the seat. A stuck seat (Status.Stuck: a
// start that never got ready, a turn gone quiet whose screen does not show
// it at work) holds nothing, as a stuck agent may need the restart; a quiet
// agent still at work (a long build, a subagent) holds the change to its
// turn's end. Nor does a relay with no seat watching it, or whose agent
// exited (a change that waited then is what its next start runs:
// applyWaitingRepick). The one rule for a change asked now (repickRelay)
// and one that waited (applyWaitingRepick, looked at again when the seat
// says it is quiet, stuck, or exited). Loop goroutine.
func (r *Runtime) heldBack(rel *state.Relay) string {
	seat := r.relays.get(rel.Name)
	if seat == nil {
		return ""
	}
	switch st := seat.Snapshot(); {
	case st.Dialog != "":
		return "dialog"
	case !st.Quiet && !st.Stuck && st.State != "exited":
		return "busy"
	}
	return ""
}

// busyWith says what keeps a relay from between turns, for the words a
// waiting change is told (the seat decides whether it waits: heldBack):
// "starting", "working" (a turn), "message" (one sent to it, on its way),
// "command" (one of its own commands, sent to it and not yet acted on), or
// "" when neither the seat nor the record can say. Loop goroutine.
func (r *Runtime) busyWith(rel *state.Relay) string {
	st := relay.Status{State: rel.State}
	if seat := r.relays.get(rel.Name); seat != nil {
		st = seat.Snapshot()
	}
	switch {
	case st.State == "starting" || rel.State == "starting":
		return "starting"
	case st.State == "working" || rel.State == "working":
		return "working"
	}
	why := ""
	for _, text := range rel.Undelivered {
		if !relay.IsAgentCommand(text) {
			return "message"
		}
		why = "command"
	}
	return why
}

// applyWaitingRepick starts a relay again with a change that waited for its
// turn to end, unless something still holds it back (heldBack). Loop
// goroutine.
func (r *Runtime) applyWaitingRepick(name string) {
	pick, ok, waiting := r.nextPick(name)
	rel := r.st.Relays[name]
	if !ok || !waiting || rel == nil {
		return
	}
	if !rel.Active() {
		r.setNextPick(name, nil, false)
		r.syncRelaySettings()
		return
	}
	if r.relays.launch(name) != nil {
		return // a start under way applies it when it is up
	}
	exited := rel.State == "exited"
	if seat := r.relays.get(name); seat != nil && seat.Snapshot().State == "exited" {
		exited = true // its seat says so before the record does (the same look)
	}
	if exited {
		// Not by starting it again: an agent that exited (the user's own
		// /exit among the ways) stays so. The change is what its next start
		// runs (relaunch), no longer one that waits, and that start is one.
		r.setNextPick(name, &pick, true)
		r.syncRelaySettings()
		r.publishRelayMeta()
		return
	}
	if r.heldBack(rel) != "" {
		return // applied once the seat says it is quiet (KindQuiet) or stuck
	}
	if err := r.restartRelayWith(rel, pick); err != nil {
		r.repickFailed(rel, pick, err)
	}
}

// repickFailed says that a change that waited could not start the agent
// again: the change is dropped. Loop goroutine.
func (r *Runtime) repickFailed(rel *state.Relay, pick relayPick, err error) {
	text := "could not start " + rel.Who() + " again with " + r.pickText(rel, pick) + ": " + err.Error()
	if errors.Is(err, errEnding) {
		// Nothing was tried: no agent starts once the session is ending.
		// Said as that, with what the agent runs.
		text = fmt.Sprintf("%s runs %s; the change to %s was not made, as the session ended first", rel.Who(), r.pickText(rel, relayPick{rel.Model, rel.Effort}), r.pickText(rel, pick))
	}
	r.append(event.New(event.Error, event.ActorHarness, event.ErrorData{Where: "relay", Text: text}))
	r.ui.Log("relay %s: %v", rel.Name, err)
}
