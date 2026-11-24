// Package runtimecontrol delivers bounded, generation-scoped local requests to
// the harness's single writer. It never appends to an active session log itself.
package runtimecontrol

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
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
	// Config is the configuration the routes were last set from as a whole
	// (a preset, or bundle:NAME); routes changed one by one since still
	// name it, so a reader can say what they were changed from.
	Config string `json:"config,omitempty"`
	// Relays are the session's relays to external agents, by name, with
	// the model and effort each runs. Changing one starts the agent again
	// on its own conversation once its current turn ends.
	Relays map[string]RelayRoute `json:"relays,omitempty"`
}

// RelayRoute is a relay's model and reasoning effort in this session.
type RelayRoute struct {
	Agent string `json:"agent"` // codex | claude
	// Model and Effort are the session's picks ("" leaves it to the
	// default below).
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// DefaultModel and DefaultEffort are what leaving a pick alone gives:
	// the relay profile's, else the agent's own configured default ("" when
	// neither says).
	DefaultModel  string `json:"default_model,omitempty"`
	DefaultEffort string `json:"default_effort,omitempty"`
	// Waiting says a change waits for the agent's current turn to end.
	Waiting bool `json:"waiting,omitempty"`
}

// Route is one actor's model route pick.
type Route struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
}

func FromConfig(c config.Config) Values {
	return FromConfigFor("", c)
}

func FromConfigFor(project string, c config.Config) Values {
	v := Values{TaskConcurrency: c.TaskConcurrency, NarratorTickSeconds: c.NarratorTickSeconds, NarratorQuietSeconds: c.NarratorQuietSeconds, Routes: map[string]Route{}}
	for actor, a := range map[string]config.Actor{"orchestrator": c.Orchestrator, "task": c.Task, "narrator": c.Narrator} {
		pid := ""
		if pr, ok := config.ProviderForURLFor(project, a.BaseURL); ok {
			pid = pr.ID
		}
		v.Routes[actor] = Route{Provider: pid, Model: a.Model, Effort: a.ReasoningEffort}
	}
	switch {
	case c.Name != "":
		v.Config = "bundle:" + c.Name
	case c.Preset != "":
		v.Config = c.Preset
	default:
		v.Config = "glm" // the built-in defaults are its lineup
	}
	return v
}

// StopOperation stops the running session, as Stop in eagent's web UI does.
// A server that does not know it (no feature session-stop.v1) is asked for
// the grant without it; see WithoutStop.
const StopOperation = "session.stop"

// WithoutStop is a session grant for a server that cannot take the stop.
func WithoutStop(g control.Grant) control.Grant {
	g.Operations = slices.DeleteFunc(slices.Clone(g.Operations), func(op string) bool { return op == StopOperation })
	return g
}

// stopAction is the stop a running session offers. What it says is what the
// app shows before the user confirms.
func stopAction(values Values) control.Action {
	text := "Ends this eagent session now, like Stop in eagent's web UI."
	names := make([]string, 0, len(values.Relays))
	for name := range values.Relays {
		names = append(names, name)
	}
	sort.Strings(names)
	who := []string{}
	for _, name := range names {
		if w := relay.AgentName(values.Relays[name].Agent); !slices.Contains(who, w) {
			who = append(who, w)
		}
	}
	switch len(who) {
	case 0:
	case 1:
		text += " " + who[0] + " stops with it."
	default:
		text += " " + strings.Join(who, " and ") + " stop with it."
	}
	text += " A reply here resumes the session while eagent serve or connector run is up."
	return control.Action{Operation: StopOperation, Label: "Stop session", Description: text, Class: "preference", Parameters: control.Shape{Type: "object"}}
}

func Grant(project, session string) (control.Grant, error) {
	if !artifact.ValidPath(session) || strings.Contains(session, "/") || len(session) > 64 {
		return control.Grant{}, fmt.Errorf("invalid session identity")
	}
	projectGrant := (&settings.Service{Project: project}).Grant()
	return control.Grant{Key: projectGrant.Key + "-session-" + session, Label: filepath.Base(project) + " · Session " + session,
		Scope: "session", Operations: []string{"settings.apply", "settings.refresh", StopOperation}, Classes: []string{"preference", "route"}}, nil
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
	// What leaving a relay's pick alone gives: shown, not saved.
	defaults := map[string]any{}
	suggestions := map[string][]control.Suggestion{}
	locked := "This runtime is unavailable. A replacement process requires its own current generation."

	// Each relay's model and effort first: in a relay session they are
	// what does the work.
	names := make([]string, 0, len(values.Relays))
	for name := range values.Relays {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rt := values.Relays[name]
		who := relay.AgentName(rt.Agent)
		group := who
		if name != rt.Agent {
			group = who + " (" + name + ")"
		}
		note := "Changing it starts " + who + " again on the same conversation, once the turn it is on ends."
		if rt.Waiting {
			note += " A change is waiting for its current turn to end."
		}
		list := rt.Agent + "_models"
		if _, ok := suggestions[list]; !ok {
			suggestions[list] = settings.RelayModelSuggestions(rt.Agent, project)
		}
		effortChoices := []any{}
		for _, e := range relay.AgentEfforts(rt.Agent) {
			effortChoices = append(effortChoices, e)
		}
		for _, spec := range []struct {
			key, label, value, fallback string
			shape                       control.Shape
			suggest                     *control.Suggest
		}{
			{"/relays/" + name + "/model", "Model", rt.Model, rt.DefaultModel, control.Shape{Type: "string", MaxLength: 200}, &control.Suggest{List: list}},
			{"/relays/" + name + "/effort", "Reasoning effort", rt.Effort, rt.DefaultEffort, control.Shape{Type: "string", Enum: effortChoices}, nil},
		} {
			description := note // once per card: under the model
			if spec.suggest == nil {
				description = ""
			}
			field := control.Field{Key: spec.key, Label: spec.label, Description: description, Section: "Models", Group: group, Shape: spec.shape, Suggest: spec.suggest, Writable: available, Unset: true, Class: "route", EffectiveWhen: "next_turn", Source: "runtime:" + generation}
			if !available {
				field.LockedReason = locked
			}
			fields = append(fields, field)
			if spec.value != "" {
				active[spec.key] = spec.value
			} else if spec.fallback != "" && spec.shape.Validate(spec.fallback) == nil {
				defaults[spec.key] = spec.fallback
			}
		}
	}

	// Per-actor model routes, editable live: provider (enum of catalog
	// providers and registered custom servers), model id (free string,
	// validated at apply time against the catalog and live cache), and
	// reasoning effort.
	providerEnum := []any{}
	for _, pr := range config.AllProviders(project) {
		providerEnum = append(providerEnum, pr.ID)
	}
	effortEnum := []any{}
	for _, e := range config.EffortOrder {
		effortEnum = append(effortEnum, e)
	}
	// The configuration the routes come from: choosing one sets every
	// actor's route to what it runs in this project.
	configs := []any{}
	for _, name := range settings.ConfigChoices(project) {
		configs = append(configs, name)
	}
	configField := control.Field{Key: "/config", Label: "Configuration", Description: "Choosing one sets every actor's provider, model and effort to what it runs in this project. This session only.", Section: "Models", Shape: control.Shape{Type: "string", Enum: configs}, Writable: available, Class: "route", EffectiveWhen: "immediate", Source: "runtime:" + generation}
	if !available {
		configField.LockedReason = "This runtime is unavailable. A replacement process requires its own current generation."
	}
	fields = append(fields, configField)
	active["/config"] = values.Config
	for _, actor := range []string{"orchestrator", "task", "narrator"} {
		rt := values.Routes[actor]
		routeField := func(key, group, label, description string, shape control.Shape) {
			field := control.Field{Key: key, Label: label, Description: description, Section: "Models", Group: group, Shape: shape, Writable: available, Unset: false, Class: "route", EffectiveWhen: "immediate", Source: "runtime:" + generation}
			if !available {
				field.LockedReason = "This runtime is unavailable. A replacement process requires its own current generation."
			}
			fields = append(fields, field)
		}
		name := strings.ToUpper(actor[:1]) + actor[1:]
		routeField("/routes/"+actor+"/provider", name, "Provider", "", control.Shape{Type: "string", Enum: providerEnum})
		routeField("/routes/"+actor+"/model", name, "Model", "", control.Shape{Type: "string", MaxLength: 200})
		routeField("/routes/"+actor+"/effort", name, "Reasoning effort", "", control.Shape{Type: "string", Enum: effortEnum})
		active["/routes/"+actor+"/provider"] = rt.Provider
		active["/routes/"+actor+"/model"] = rt.Model
		active["/routes/"+actor+"/effort"] = rt.Effort
	}
	for _, spec := range []struct {
		key, group, label, effect, description string
		value, minimum, maximum                int
	}{
		{"/task_concurrency", "Work", "Tasks at once", "next_task", "Queued tasks start as workers free up; running tasks continue.", values.TaskConcurrency, 1, 128},
		{"/narrator_tick_seconds", "Narrator", "Check interval (seconds)", "immediate", "How often the narrator looks in while work goes on.", values.NarratorTickSeconds, 1, 86400},
		{"/narrator_quiet_seconds", "Narrator", "Quiet limit (seconds)", "next_turn", "The longest you go without a progress line while work continues. 0 turns the reminder off.", values.NarratorQuietSeconds, 0, 86400},
	} {
		minimum, maximum := float64(spec.minimum), float64(spec.maximum)
		field := control.Field{Key: spec.key, Label: spec.label, Description: spec.description, Section: "Behavior", Group: spec.group, Shape: control.Shape{Type: "integer", Minimum: &minimum, Maximum: &maximum}, Writable: available, Unset: false, Class: "preference", EffectiveWhen: spec.effect, Source: "runtime:" + generation}
		if !available {
			field.LockedReason = "This runtime is unavailable. A replacement process requires its own current generation."
		}
		fields = append(fields, field)
		active[spec.key] = spec.value
	}
	raw, _ := json.Marshal(map[string]any{"schema": Schema, "generation": generation, "values": values})
	version := artifact.Digest(raw)
	effective := map[string]any{}
	for k, v := range active {
		effective[k] = v
	}
	for k, v := range defaults {
		effective[k] = v
	}
	descriptor := control.Descriptor{Format: control.Format, SchemaVersion: Schema, AdapterVersion: "1", Fields: fields}
	if len(suggestions) > 0 {
		descriptor.Suggestions = suggestions
	}
	// Only a running session can be stopped. Publication drops the action
	// where the connector's grant does not allow it (see integration).
	if available {
		descriptor.Actions = []control.Action{stopAction(values)}
	}
	return settings.RemoteView{Key: grant.Key, Label: grant.Label, Scope: "session", Generation: generation,
		Descriptor: descriptor,
		Snapshot: control.Snapshot{Version: version, Context: project + " · " + session, Saved: active, Effective: effective, RuntimeKnown: available, RuntimeVersion: version,
			Details: map[string]any{"session_id": session, "thread_external_id": "eagent:" + session, "persistence": "This process only. Project defaults and future or resumed sessions are unchanged."}}}, nil
}

func ApplyValues(project string, before Values, proposal control.Proposal, view settings.RemoteView, grant control.Grant) (Values, error) {
	if proposal.Generation != view.Generation || proposal.ExpectedVersion != view.Snapshot.Version || !view.Snapshot.RuntimeKnown {
		return before, fmt.Errorf("runtime generation, version or availability changed")
	}
	if err := proposal.Validate(view.Descriptor, grant); err != nil {
		return before, err
	}
	if proposal.Operation == "settings.refresh" || proposal.Operation == StopOperation {
		return before, nil
	}
	// Copy before mutating: before.Routes is shared with the controller's
	// live Values and must not change under a rejected proposal either.
	routes := map[string]Route{}
	for k, v := range before.Routes {
		routes[k] = v
	}
	var relays map[string]RelayRoute
	if before.Relays != nil {
		relays = map[string]RelayRoute{}
		for k, v := range before.Relays {
			relays[k] = v
		}
	}
	touched := map[string]bool{}
	// A configuration chosen sets every route to what it runs here; route
	// edits in the same proposal then apply on top of it.
	for _, edit := range proposal.Edits {
		if edit.Key != "/config" {
			continue
		}
		name, ok := edit.Value.(string)
		if edit.Op != "set" || !ok {
			return before, fmt.Errorf("choose a configuration by name")
		}
		preset, bundle := name, ""
		if b, isBundle := strings.CutPrefix(name, "bundle:"); isBundle {
			preset, bundle = "", b
		}
		cfg, err := config.LoadBundle(project, preset, bundle)
		if err != nil {
			return before, fmt.Errorf("configuration %s: %w", name, err)
		}
		for actor, rt := range FromConfigFor(project, cfg).Routes {
			routes[actor] = rt
			touched[actor] = true
		}
		before.Config = name
	}
	for _, edit := range proposal.Edits {
		if edit.Key == "/config" {
			continue
		}
		if rest, ok := strings.CutPrefix(edit.Key, "/relays/"); ok {
			name, field, _ := strings.Cut(rest, "/")
			rt, exists := relays[name]
			if !exists {
				return before, fmt.Errorf("no relay %q in this session", name)
			}
			value := ""
			switch edit.Op {
			case "set":
				s, ok := edit.Value.(string)
				if !ok || strings.TrimSpace(s) == "" || strings.ContainsAny(s, " \t\n") {
					return before, fmt.Errorf("%s must be a model or effort name", edit.Key)
				}
				value = s
			case "unset":
			default:
				return before, fmt.Errorf("unsupported edit %q", edit.Op)
			}
			switch field {
			case "model":
				rt.Model = value
			case "effort":
				rt.Effort = value
			default:
				return before, fmt.Errorf("unsupported relay setting %q", edit.Key)
			}
			relays[name] = rt
			continue
		}
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
		if picked.APIKeyEnv != "" && os.Getenv(picked.APIKeyEnv) == "" {
			return before, fmt.Errorf("%s is not set where this session runs, so the %s cannot use %s", picked.APIKeyEnv, actor, rt.Provider)
		}
	}
	before.Routes = routes
	before.Relays = relays
	before.Revision++
	return before, nil
}
