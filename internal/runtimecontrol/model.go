// Package runtimecontrol delivers bounded, generation-scoped local requests to
// the harness's single writer. It never appends to an active session log itself.
package runtimecontrol

import (
	"encoding/json"
	"fmt"
	"os"
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
	// Routes is each actor's model route for this running session, as a
	// provider id + model id + reasoning effort. Changed at runtime through
	// the session-scoped settings resource; applied between turns.
	Routes map[string]Route `json:"routes,omitempty"`
}

// Route is one actor's model route pick.
type Route struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
}

func FromConfig(c config.Config) Values {
	v := Values{TaskConcurrency: c.TaskConcurrency, NarratorTickSeconds: c.NarratorTickSeconds, NarratorQuietSeconds: c.NarratorQuietSeconds, Routes: map[string]Route{}}
	for actor, a := range map[string]config.Actor{"orchestrator": c.Orchestrator, "task": c.Task, "narrator": c.Narrator} {
		pid := ""
		if pr, ok := config.ProviderForURL(a.BaseURL); ok {
			pid = pr.ID
		}
		v.Routes[actor] = Route{Provider: pid, Model: a.Model, Effort: a.ReasoningEffort}
	}
	return v
}

func Grant(project, session string) (control.Grant, error) {
	if !artifact.ValidPath(session) || strings.Contains(session, "/") || len(session) > 64 {
		return control.Grant{}, fmt.Errorf("invalid session identity")
	}
	projectGrant := (&settings.Service{Project: project}).Grant()
	return control.Grant{Key: projectGrant.Key + "-session-" + session, Label: filepath.Base(project) + " · Session " + session,
		Scope: "session", Operations: []string{"settings.apply", "settings.refresh"}, Classes: []string{"preference", "route"}}, nil
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
	// Per-actor model routes, editable live: provider (enum of catalog
	// providers), model id (free string, validated at apply time against the
	// catalog and the live model cache), and reasoning effort.
	providerEnum := []any{}
	for _, pr := range config.Providers {
		providerEnum = append(providerEnum, pr.ID)
	}
	effortEnum := []any{}
	for _, e := range config.EffortOrder {
		effortEnum = append(effortEnum, e)
	}
	for _, actor := range []string{"orchestrator", "task", "narrator"} {
		rt := values.Routes[actor]
		routeField := func(key, label, description string, shape control.Shape) {
			field := control.Field{Key: key, Label: label, Description: description, Shape: shape, Writable: available, Unset: false, Class: "route", EffectiveWhen: "immediate", Source: "runtime:" + generation}
			if !available {
				field.LockedReason = "This runtime is unavailable. A replacement process requires its own current generation."
			}
			fields = append(fields, field)
		}
		name := strings.ToUpper(actor[:1]) + actor[1:]
		routeField("/routes/"+actor+"/provider", name+" provider", "Catalog provider whose base URL and key variable the route uses.", control.Shape{Type: "string", Enum: providerEnum})
		routeField("/routes/"+actor+"/model", name+" model", "Model id at that provider (see the Config page picker for the full list, live models included).", control.Shape{Type: "string", MaxLength: 200})
		routeField("/routes/"+actor+"/effort", name+" reasoning effort", "", control.Shape{Type: "string", Enum: effortEnum})
		active["/routes/"+actor+"/provider"] = rt.Provider
		active["/routes/"+actor+"/model"] = rt.Model
		active["/routes/"+actor+"/effort"] = rt.Effort
	}
	raw, _ := json.Marshal(map[string]any{"schema": Schema, "generation": generation, "values": values})
	version := artifact.Digest(raw)
	return settings.RemoteView{Key: grant.Key, Label: grant.Label, Scope: "session", Generation: generation,
		Descriptor: control.Descriptor{Format: control.Format, SchemaVersion: Schema, AdapterVersion: "1", Fields: fields},
		Snapshot: control.Snapshot{Version: version, Context: project + " · " + session, Saved: active, Effective: active, RuntimeKnown: available, RuntimeVersion: version,
			Details: map[string]any{"session_id": session, "thread_external_id": "eagent:" + session, "persistence": "This process only. Project defaults and future or resumed sessions are unchanged."}}}, nil
}

func ApplyValues(project string, before Values, proposal control.Proposal, view settings.RemoteView, grant control.Grant) (Values, error) {
	if proposal.Generation != view.Generation || proposal.ExpectedVersion != view.Snapshot.Version || !view.Snapshot.RuntimeKnown {
		return before, fmt.Errorf("runtime generation, version or availability changed")
	}
	if err := proposal.Validate(view.Descriptor, grant); err != nil {
		return before, err
	}
	if proposal.Operation == "settings.refresh" {
		return before, nil
	}
	// Copy before mutating: before.Routes is shared with the controller's
	// live Values and must not change under a rejected proposal either.
	routes := map[string]Route{}
	for k, v := range before.Routes {
		routes[k] = v
	}
	touched := map[string]bool{}
	for _, edit := range proposal.Edits {
		if strings.HasPrefix(edit.Key, "/routes/") {
			parts := strings.Split(strings.TrimPrefix(edit.Key, "/routes/"), "/")
			if len(parts) != 2 {
				return before, fmt.Errorf("unsupported runtime setting %q", edit.Key)
			}
			actor, field := parts[0], parts[1]
			if actor != "orchestrator" && actor != "task" && actor != "narrator" {
				return before, fmt.Errorf("unknown actor %q", actor)
			}
			if edit.Op != "set" {
				return before, fmt.Errorf("route fields cannot be unset; set them instead")
			}
			s, ok := edit.Value.(string)
			if !ok || s == "" {
				return before, fmt.Errorf("%s must be a non-empty string", edit.Key)
			}
			rt := routes[actor]
			switch field {
			case "provider":
				rt.Provider = s
			case "model":
				rt.Model = s
			case "effort":
				rt.Effort = s
			default:
				return before, fmt.Errorf("unsupported route field %q", field)
			}
			routes[actor] = rt
			touched[actor] = true
			continue
		}
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
	// Validate the final triplets of actors touched by this proposal, after
	// all edits are merged. Untouched routes pass through as they were.
	for actor := range touched {
		rt := routes[actor]
		if rt.Provider == "" || rt.Model == "" {
			return before, fmt.Errorf("actor %s needs both a provider and a model", actor)
		}
		picked, err := config.PickRoute(project, config.Actor{}, rt.Provider, rt.Model, rt.Effort)
		if err != nil {
			return before, err
		}
		if os.Getenv(picked.APIKeyEnv) == "" {
			return before, fmt.Errorf("%s is not set where this session runs, so the %s cannot use %s", picked.APIKeyEnv, actor, rt.Provider)
		}
	}
	before.Routes = routes
	before.Revision++
	return before, nil
}
