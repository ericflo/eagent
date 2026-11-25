package config

import (
	"encoding/json"
	"sort"
	"strings"
)

// eagent used to run three models (an orchestrator, a task worker and a
// narrator) and now runs one. Configuration files written for that design
// keep working: their keys are carried over each time a file is read, and
// the file is rewritten in today's keys the next time it is saved.

// legacyDropped are keys with nothing to carry over to.
var legacyDropped = []string{
	"task", "task_concurrency", "max_task_turns",
	"bash_wait_seconds", "bash_timeout_seconds", "tool_output_max_chars", "allow_outside_project",
}

// legacyRenamed are settings that only changed name.
var legacyRenamed = map[string]string{
	"narrator_tick_seconds":           "tick_seconds",
	"narrator_quiet_seconds":          "quiet_seconds",
	"max_orchestrator_calls_per_turn": "max_calls_per_turn",
}

// HasLegacyKeys reports whether a configuration object uses keys of the
// three-actor design.
func HasLegacyKeys(m map[string]json.RawMessage) bool {
	if _, ok := m["orchestrator"]; ok {
		return true
	}
	if _, ok := m["narrator"]; ok {
		return true
	}
	for _, k := range legacyDropped {
		if _, ok := m[k]; ok {
			return true
		}
	}
	for k := range legacyRenamed {
		if _, ok := m[k]; ok {
			return true
		}
	}
	if relay := object(m["relay"]); relay != nil {
		if _, ok := relay["quiet_seconds"]; ok {
			return true
		}
	}
	return false
}

// MigrateLegacy returns a configuration object with the keys of the
// three-actor design carried over to today's, and one line for each thing it
// did. The input is not changed; an object with no such keys is returned as
// it is, with no notes.
//
//   - orchestrator becomes model. The one model has one effort for every
//     call, where the orchestrator and the narrator each had their own. An
//     effort the file names for one of them, or the same for both, is kept;
//     two different ones become medium, between the two.
//   - narrator_tick_seconds and narrator_quiet_seconds become tick_seconds and
//     quiet_seconds; relay.quiet_seconds, which replaced the quiet limit in
//     relay sessions, becomes quiet_seconds too and wins over
//     narrator_quiet_seconds, since every session is a relay now. A key
//     already in today's name is never overwritten.
//   - task, narrator, and the settings of the task worker and the shell tools
//     are dropped.
func MigrateLegacy(m map[string]json.RawMessage) (map[string]json.RawMessage, []string) {
	if !HasLegacyKeys(m) {
		return m, nil
	}
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		out[k] = v
	}
	var notes []string

	orch, narr := object(out["orchestrator"]), object(out["narrator"])
	_, hasOrch := out["orchestrator"]
	_, hasNarr := out["narrator"]
	if _, hasModel := out["model"]; !hasModel && (orch != nil || narr != nil) {
		model := map[string]json.RawMessage{}
		for k, v := range orch {
			model[k] = v
		}
		// One model has one effort. An effort the file names is kept when
		// it names only one, or the same for both; when the orchestrator
		// and the narrator were given different ones it becomes medium,
		// between the two, and the note says so.
		oe, hasOE := orch["reasoning_effort"]
		ne, hasNE := narr["reasoning_effort"]
		var effort json.RawMessage
		said := ""
		switch {
		case hasOE && hasNE && string(oe) == string(ne):
			effort, said = oe, "; its reasoning effort stays "+strings.Trim(string(oe), `"`)
		case hasOE && hasNE:
			effort = json.RawMessage(`"` + loopEffort + `"`)
			said = "; its reasoning effort is " + loopEffort + " (the orchestrator had " + strings.Trim(string(oe), `"`) + " and the narrator " + strings.Trim(string(ne), `"`) + ", and one model has one effort; set model.reasoning_effort to change it)"
		case hasOE:
			effort, said = oe, "; its reasoning effort stays "+strings.Trim(string(oe), `"`)+", now for every call"
		case hasNE:
			effort, said = ne, "; its reasoning effort is the narrator's, "+strings.Trim(string(ne), `"`)
		}
		if effort != nil {
			setEffort(model, effort)
		}
		if len(model) > 0 {
			out["model"], _ = json.Marshal(model)
			if orch != nil {
				notes = append(notes, "orchestrator is now model, the one model for every call"+said)
			} else {
				notes = append(notes, "the narrator's reasoning effort is now model.reasoning_effort")
			}
		}
	}
	if hasOrch {
		delete(out, "orchestrator")
	}
	if hasNarr {
		delete(out, "narrator")
		notes = append(notes, "narrator was dropped: one model does the talking now")
	}

	var renamed []string
	for old := range legacyRenamed {
		renamed = append(renamed, old)
	}
	sort.Strings(renamed)
	for _, old := range renamed {
		v, ok := out[old]
		if !ok {
			continue
		}
		delete(out, old)
		if _, taken := out[legacyRenamed[old]]; !taken {
			out[legacyRenamed[old]] = v
		}
		notes = append(notes, old+" is now "+legacyRenamed[old])
	}
	if relay := object(out["relay"]); relay != nil {
		if v, ok := relay["quiet_seconds"]; ok {
			delete(relay, "quiet_seconds")
			// It wins over narrator_quiet_seconds, but a quiet_seconds the
			// file already has in today's key stays.
			if _, current := m["quiet_seconds"]; !current && string(v) != "0" && string(v) != "null" {
				out["quiet_seconds"] = v
				notes = append(notes, "relay.quiet_seconds is now quiet_seconds")
			}
			if len(relay) == 0 {
				delete(out, "relay")
			} else {
				out["relay"], _ = json.Marshal(relay)
			}
		}
	}
	for _, k := range legacyDropped {
		if _, ok := out[k]; ok {
			delete(out, k)
			notes = append(notes, k+" was dropped: it has no use now")
		}
	}
	return out, notes
}

// setEffort gives a route, and its fallbacks all the way down, one effort.
func setEffort(route map[string]json.RawMessage, effort json.RawMessage) {
	route["reasoning_effort"] = effort
	if fb := object(route["fallback"]); fb != nil {
		setEffort(fb, effort)
		route["fallback"], _ = json.Marshal(fb)
	}
}

// object decodes a JSON object, or returns nil for anything else.
func object(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}
