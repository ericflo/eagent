package config

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// eagent used to run three models (an orchestrator, a task worker and a
// narrator) and now runs one. Configuration files written for that design
// keep working: their keys are carried over each time a file is read, and
// the file is rewritten in today's keys the next time it is saved. Every key
// that is dropped or changes meaning gets a note in Config.Migrated.

// legacyDropped are keys with nothing to carry over to, in the order their
// notes are written, with the reason each one went.
var legacyDropped = []struct{ key, why string }{
	{"task", "there are no task workers now; the coding agent does the work"},
	{"task_concurrency", "there are no task workers now"},
	{"max_task_turns", "there are no task workers now"},
	{"bash_wait_seconds", "eagent has no shell tool now; the coding agent runs commands"},
	{"bash_timeout_seconds", "eagent has no shell tool now; the coding agent runs commands"},
	{"tool_output_max_chars", "eagent has no shell or file tools now"},
	{"allow_outside_project", "eagent has no file tools now; the coding agent reads and writes files"},
}

// legacyRenamed are settings that only changed name.
var legacyRenamed = map[string]string{
	"narrator_tick_seconds":           "tick_seconds",
	"narrator_quiet_seconds":          "quiet_seconds",
	"max_orchestrator_calls_per_turn": "max_calls_per_turn",
}

// legacyRolloverTokens is the rollover default of the three-actor design.
// Some of its presets used legacyPresetRolloverTokens.
const (
	legacyRolloverTokens       = 150_000
	legacyPresetRolloverTokens = 300_000
)

// maxCallsPerTurnLimit is the most max_calls_per_turn allows.
const maxCallsPerTurnLimit = 200

// HasLegacyKeys reports whether a configuration object uses keys of the
// three-actor design.
func HasLegacyKeys(m map[string]json.RawMessage) bool {
	if _, ok := m["orchestrator"]; ok {
		return true
	}
	if _, ok := m["narrator"]; ok {
		return true
	}
	for _, d := range legacyDropped {
		if _, ok := m[d.key]; ok {
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
// did or dropped. The input is not changed; an object with no such keys is
// returned as it is, with no notes.
//
//   - orchestrator becomes model, unless the object already has model, in
//     which case orchestrator is ignored. The one model has one effort for
//     every call, where the orchestrator and the narrator each had their own:
//     the orchestrator's effort is kept whenever the file names it, since
//     the one model makes the orchestrator's decisions. The narrator's is
//     never carried over; with no orchestrator effort in the file the
//     preset's applies, which is the level its orchestrator ran at.
//   - narrator_tick_seconds and narrator_quiet_seconds become tick_seconds and
//     quiet_seconds; relay.quiet_seconds, which replaced the quiet limit in
//     relay sessions, becomes quiet_seconds too and wins over
//     narrator_quiet_seconds, since every session is a relay now. A key
//     already in today's name is never overwritten.
//   - max_orchestrator_calls_per_turn becomes max_calls_per_turn, which now
//     ends a turn where the old one only reminded; a value above 200 becomes
//     200, the most the new key allows.
//   - task, narrator, and the settings of the task worker and the shell tools
//     are dropped.
//   - rollover_tokens keeps its value; a file that does not name it is told
//     that the default now follows the model's context window.
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
	_, hasModel := out["model"]
	oe, hasOE := orch["reasoning_effort"]
	ne, hasNE := narr["reasoning_effort"]
	effortName := func(raw json.RawMessage) string { return strings.Trim(string(raw), `"`) }
	switch {
	case hasModel && hasOrch:
		notes = append(notes, "orchestrator was ignored: the file also has model, which is used")
	case orch != nil:
		model := map[string]json.RawMessage{}
		for k, v := range orch {
			model[k] = v
		}
		// The route is carried over as the file wrote it, fallbacks and
		// their efforts included.
		said := ""
		if hasOE {
			said = "; its reasoning effort stays " + effortName(oe) + ", now for every call"
		}
		if len(model) > 0 {
			out["model"], _ = json.Marshal(model)
			notes = append(notes, "orchestrator is now model, the one model for every call"+said)
		}
	}
	switch carried := hasOE && !hasModel; {
	case !hasNE, carried && string(oe) == string(ne):
		// No narrator effort, or the same level as the orchestrator's.
	case carried:
		notes = append(notes, "narrator.reasoning_effort ("+effortName(ne)+") is not carried over: one model has one effort, and it is the orchestrator's ("+effortName(oe)+
			"). Set model.reasoning_effort to choose another level.")
	default:
		notes = append(notes, "narrator.reasoning_effort ("+effortName(ne)+") is not carried over: the one model does the orchestrator's work too, at the effort the orchestrator ran at. Set model.reasoning_effort to choose another level.")
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
		now := legacyRenamed[old]
		if _, taken := out[now]; taken {
			notes = append(notes, old+" was ignored: the file also has "+now+", which is used")
			continue
		}
		if old != "max_orchestrator_calls_per_turn" {
			out[now] = v
			notes = append(notes, old+" is now "+now)
			continue
		}
		notes = append(notes, "max_orchestrator_calls_per_turn is now max_calls_per_turn, and it is now where a turn stops (it was only a reminder)")
		if n, err := strconv.Atoi(strings.TrimSpace(string(v))); err == nil && n > maxCallsPerTurnLimit {
			v = json.RawMessage(strconv.Itoa(maxCallsPerTurnLimit))
			notes = append(notes, "max_orchestrator_calls_per_turn ("+strconv.Itoa(n)+") is above "+strconv.Itoa(maxCallsPerTurnLimit)+", the most max_calls_per_turn allows, so it is "+strconv.Itoa(maxCallsPerTurnLimit))
		}
		out[now] = v
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
	for _, d := range legacyDropped {
		if _, ok := out[d.key]; ok {
			delete(out, d.key)
			notes = append(notes, d.key+" was dropped: "+d.why)
		}
	}
	if _, ok := out["rollover_tokens"]; !ok {
		notes = append(notes, "rollover_tokens is not set in this file, so the conversation now restarts at half the model's context window where the default was "+
			strconv.Itoa(legacyRolloverTokens)+" ("+strconv.Itoa(legacyPresetRolloverTokens)+" in some presets); set rollover_tokens to fix a number")
	}
	return out, notes
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
