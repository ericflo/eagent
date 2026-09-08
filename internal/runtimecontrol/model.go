// Package runtimecontrol delivers bounded, generation-scoped local requests to
// the harness's single writer. It never appends to an active session log itself.
package runtimecontrol

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

const Schema = "eagent.runtime-settings/v1"
const EventType = "settings.changed"

type Values struct {
	Revision             uint64 `json:"revision"`
	TaskConcurrency      int    `json:"task_concurrency"`
	NarratorTickSeconds  int    `json:"narrator_tick_seconds"`
	NarratorQuietSeconds int    `json:"narrator_quiet_seconds"`
}

func FromConfig(c config.Config) Values {
	return Values{TaskConcurrency: c.TaskConcurrency, NarratorTickSeconds: c.NarratorTickSeconds, NarratorQuietSeconds: c.NarratorQuietSeconds}
}

func Grant(project, session string) (control.Grant, error) {
	if !artifact.ValidPath(session) || strings.Contains(session, "/") || len(session) > 64 {
		return control.Grant{}, fmt.Errorf("invalid session identity")
	}
	projectGrant := (&settings.Service{Project: project}).Grant()
	return control.Grant{Key: projectGrant.Key + "-session-" + session, Label: filepath.Base(project) + " · Session " + session,
		Scope: "session", Operations: []string{"settings.apply", "settings.refresh"}, Classes: []string{"preference"}}, nil
}

type Status struct {
	Session        string    `json:"session_id"`
	Generation     string    `json:"generation"`
	AvailableUntil time.Time `json:"available_until"`
	Values         Values    `json:"values"`
}

func View(project, session, generation string, values Values, available bool) (settings.RemoteView, error) {
	grant, err := Grant(project, session)
	if err != nil {
		return settings.RemoteView{}, err
	}
	fields := []control.Field{}
	active := map[string]any{}
	for _, spec := range []struct {
		key, label, effect, description string
		value, minimum, maximum         int
	}{
		{"/task_concurrency", "Task workers at once", "next_task", "Changes admission of queued tasks; currently running tasks continue.", values.TaskConcurrency, 1, 128},
		{"/narrator_tick_seconds", "Narrator check interval (seconds)", "immediate", "Reschedules the next narrator check without interrupting a current turn.", values.NarratorTickSeconds, 1, 86400},
		{"/narrator_quiet_seconds", "Narrator quiet limit (seconds)", "next_turn", "Used at the next narrator check and when preparing its next turn; zero disables the quiet-limit wake.", values.NarratorQuietSeconds, 0, 86400},
	} {
		minimum, maximum := float64(spec.minimum), float64(spec.maximum)
		field := control.Field{Key: spec.key, Label: spec.label, Description: spec.description, Shape: control.Shape{Type: "integer", Minimum: &minimum, Maximum: &maximum}, Writable: available, Unset: false, Class: "preference", EffectiveWhen: spec.effect, Source: "runtime:" + generation}
		if !available {
			field.LockedReason = "This runtime is unavailable. A replacement process requires its own current generation."
		}
		fields = append(fields, field)
		active[spec.key] = spec.value
	}
	raw, _ := json.Marshal(map[string]any{"schema": Schema, "generation": generation, "values": values})
	version := artifact.Digest(raw)
	return settings.RemoteView{Key: grant.Key, Label: grant.Label, Scope: "session", Generation: generation,
		Descriptor: control.Descriptor{Format: control.Format, SchemaVersion: Schema, AdapterVersion: "1", Fields: fields},
		Snapshot: control.Snapshot{Version: version, Context: project + " · " + session, Saved: active, Effective: active, RuntimeKnown: available, RuntimeVersion: version,
			Details: map[string]any{"session_id": session, "thread_external_id": "eagent:" + session, "persistence": "This process only. Project defaults and future or resumed sessions are unchanged."}}}, nil
}

func ApplyValues(before Values, proposal control.Proposal, view settings.RemoteView, grant control.Grant) (Values, error) {
	if proposal.Generation != view.Generation || proposal.ExpectedVersion != view.Snapshot.Version || !view.Snapshot.RuntimeKnown {
		return before, fmt.Errorf("runtime generation, version or availability changed")
	}
	if err := proposal.Validate(view.Descriptor, grant); err != nil {
		return before, err
	}
	if proposal.Operation == "settings.refresh" {
		return before, nil
	}
	for _, edit := range proposal.Edits {
		raw, _ := json.Marshal(edit.Value)
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			return before, err
		}
		switch edit.Key {
		case "/task_concurrency":
			before.TaskConcurrency = value
		case "/narrator_tick_seconds":
			before.NarratorTickSeconds = value
		case "/narrator_quiet_seconds":
			before.NarratorQuietSeconds = value
		default:
			return before, fmt.Errorf("unsupported runtime setting")
		}
	}
	before.Revision++
	return before, nil
}
