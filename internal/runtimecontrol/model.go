// Package runtimecontrol delivers bounded, generation-scoped local requests to
// the harness's single writer. It never appends to an active session log itself.
package runtimecontrol

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/settings"
)

const Schema = "eagent.runtime-settings/v2"
const EventType = "settings.changed"

// Route keys in Values.Routes.
const (
	// RouteEagent is the route of the model eagent runs on.
	RouteEagent = "eagent"
	// RouteSummarizer is the dedicated summarizer's route, present only when
	// the session's summarizer is dedicated.
	RouteSummarizer = "summarizer"
)

// routeOrder is the order route cards are shown in.
var routeOrder = []string{RouteEagent, RouteSummarizer}

// routeLabels are what each route's card is called. A reader puts the card's
// name before a field's label ("eagent model", "Summarizer reasoning
// effort"), so eagent's card is named for eagent, not "Model".
var routeLabels = map[string]string{RouteEagent: "eagent", RouteSummarizer: "Summarizer"}

// routeNames name each route in a sentence.
var routeNames = map[string]string{RouteEagent: "eagent's model", RouteSummarizer: "the summarizer"}

type Values struct {
	Revision uint64 `json:"revision"`
	// TickSeconds is how often eagent considers saying something while the
	// agent works; QuietSeconds is the longest the user goes without a
	// progress line (0: no limit).
	TickSeconds  int `json:"tick_seconds"`
	QuietSeconds int `json:"quiet_seconds"`
	// Routes are this running session's model routes, each as a provider id
	// + model id + reasoning effort: RouteEagent always, RouteSummarizer only
	// when the summarizer is dedicated. Changed at runtime through the
	// session-scoped settings resource; applied between turns.
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

// Route is one model route pick.
type Route struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
}

func FromConfig(c config.Config) Values {
	return FromConfigFor("", c)
}

func FromConfigFor(project string, c config.Config) Values {
	v := Values{TickSeconds: c.TickSeconds, QuietSeconds: c.QuietSeconds, Routes: map[string]Route{RouteEagent: routeFor(project, c.Model)}}
	if c.Summarizer.Mode == config.SummarizerDedicated {
		v.Routes[RouteSummarizer] = routeFor(project, c.Summarizer.Route(c.Model))
	}
	switch {
	case c.Name != "":
		v.Config = "bundle:" + c.Name
	case c.Preset != "":
		v.Config = c.Preset
	default:
		v.Config = "glm" // the built-in defaults are its model
	}
	return v
}

// routeFor is a configured route as a pick.
func routeFor(project string, a config.ModelRoute) Route {
	pid := ""
	if pr, ok := config.ProviderForURLFor(project, a.BaseURL); ok {
		pid = pr.ID
	}
	return Route{Provider: pid, Model: a.Model, Effort: a.ReasoningEffort}
}

// Names reports whether rt is a pick of the configured route a: its model
// at its provider's address (no provider, for an address none knows).
func Names(project string, a config.ModelRoute, rt Route) bool {
	return a.Model == rt.Model && routeFor(project, a).Provider == rt.Provider
}

// ChainEntry is the entry of a configured chain (a route and its
// fallbacks) that rt picks, with the fallbacks after it. A pick of a model
// the chain already has starts from that entry, so its own limits and key
// variable carry over rather than the first route's or the catalog's.
func ChainEntry(project string, chain config.ModelRoute, rt Route) (config.ModelRoute, bool) {
	for cur := &chain; cur != nil; cur = cur.Fallback {
		if Names(project, *cur, rt) {
			return *cur, true
		}
	}
	return config.ModelRoute{}, false
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

	// eagent's model routes, editable live: provider (enum of catalog
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
	for _, key := range routeOrder {
		rt, ok := values.Routes[key]
		if !ok {
			continue
		}
		routeField := func(field, label string, shape control.Shape) {
			f := control.Field{Key: "/routes/" + key + "/" + field, Label: label, Section: "Models", Group: routeLabels[key], Shape: shape, Writable: available, Unset: false, Class: "route", EffectiveWhen: "immediate", Source: "runtime:" + generation}
			if !available {
				f.LockedReason = locked
			}
			fields = append(fields, f)
		}
		routeField("provider", "Provider", control.Shape{Type: "string", Enum: providerEnum})
		routeField("model", "Model", control.Shape{Type: "string", MaxLength: 200})
		routeField("effort", "Reasoning effort", control.Shape{Type: "string", Enum: effortEnum})
		active["/routes/"+key+"/provider"] = rt.Provider
		active["/routes/"+key+"/model"] = rt.Model
		active["/routes/"+key+"/effort"] = rt.Effort

	}
	for _, spec := range []struct {
		key, label, effect, description string
		value, minimum, maximum         int
	}{
		{"/tick_seconds", "Seconds between progress looks", "immediate", "How often eagent considers telling you how the work is going. A look with nothing new costs nothing.", values.TickSeconds, 5, 86400},
		{"/quiet_seconds", "Longest silence while the agent works", "next_turn", "In seconds: the longest you go without a progress line. 0 turns the limit off.", values.QuietSeconds, 0, 86400},
	} {
		minimum, maximum := float64(spec.minimum), float64(spec.maximum)
		field := control.Field{Key: spec.key, Label: spec.label, Description: spec.description, Section: "Behavior", Group: "Progress", Shape: control.Shape{Type: "integer", Minimum: &minimum, Maximum: &maximum}, Writable: available, Unset: false, Class: "preference", EffectiveWhen: spec.effect, Source: "runtime:" + generation}
		if !available {
			field.LockedReason = locked
		}
		fields = append(fields, field)
		active[spec.key] = spec.value
	}

	// Each agent's own model and effort.
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
		list := relay.ModelListName(rt.Agent)
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

	// The configuration the routes come from: choosing one sets the
	// session's routes to what it runs in this project.
	configs := []any{}
	for _, name := range settings.ConfigChoices(project) {
		configs = append(configs, name)
	}
	configField := control.Field{Key: "/config", Label: "Configuration", Description: "Choosing one sets eagent's provider, model and effort to what it runs in this project. This session only.", Section: "Models", Group: "Configuration", Shape: control.Shape{Type: "string", Enum: configs}, Writable: available, Class: "route", EffectiveWhen: "immediate", Source: "runtime:" + generation}
	savePresetField := control.Field{Key: "/save_preset", Label: "Save as new preset", Description: "Type a name (letters, digits, '-', '_', '.') and tap Save to save this session's models and settings as a reusable preset in this project.", Section: "Models", Group: "Configuration", Shape: control.Shape{Type: "string", MaxLength: 64}, Writable: available, Unset: true, Class: "route", EffectiveWhen: "immediate", Source: "runtime:" + generation}
	if !available {
		configField.LockedReason = locked
		savePresetField.LockedReason = locked
	}
	fields = append(fields, configField, savePresetField)
	active["/config"] = values.Config
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

// ApplyValues is the session's values with proposal applied, for a caller
// that does not have the session's routes (the connector): the key a
// route needs and a route at an address no provider knows are left to the
// session (applyValues).
func ApplyValues(project string, before Values, proposal control.Proposal, view settings.RemoteView, grant control.Grant) (Values, error) {
	return applyValues(project, before, proposal, view, grant, nil)
}

// applyValues is ApplyValues where running (Controller.Routes) gives the
// routes the session will run the picks on, as configured: their key
// variables are the ones checked.
func applyValues(project string, before Values, proposal control.Proposal, view settings.RemoteView, grant control.Grant, running func(map[string]Route) (map[string]config.ModelRoute, error)) (Values, error) {
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
	// effortNamed marks the routes whose effort this proposal names; any
	// other touched route keeps its effort where the new model offers it,
	// else takes the nearest it does.
	effortNamed := map[string]bool{}
	savePreset := ""
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
		var cfg config.Config
		var err error
		if b, isBundle := strings.CutPrefix(name, "bundle:"); isBundle {
			cfg, err = config.LoadBundle(project, "", b)
		} else {
			cfg, err = config.ChosenPreset(project, name)
		}
		if err != nil {
			return before, fmt.Errorf("configuration %s: %w", name, err)
		}
		// The session keeps the routes it has: a summarizer route, when
		// there is one, takes the one the configuration would give it if
		// the configuration has a dedicated summarizer (as sessionImplies
		// says), and is kept otherwise.
		chosen := map[string]Route{RouteEagent: routeFor(project, cfg.Model), RouteSummarizer: routeFor(project, cfg.Summarizer.Route(cfg.Model))}
		for key := range routes {
			if key == RouteSummarizer && cfg.Summarizer.Mode != config.SummarizerDedicated {
				continue
			}
			routes[key] = chosen[key]
			touched[key] = true
		}
		before.Config = name
	}
	for _, edit := range proposal.Edits {
		if edit.Key == "/config" {
			continue
		}
		if edit.Key == "/save_preset" {
			if edit.Op == "unset" {
				continue
			}
			if edit.Op != "set" {
				return before, fmt.Errorf("unsupported edit %q", edit.Op)
			}
			rawName, _ := edit.Value.(string)
			name, err := settings.ValidateSavePresetName(rawName)
			if err != nil {
				return before, err
			}
			savePreset = name
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
			key, field := parts[0], parts[1]
			if _, ok := routes[key]; !ok {
				return before, fmt.Errorf("no route %q in this session", key)
			}
			if edit.Op != "set" {
				return before, fmt.Errorf("route fields cannot be unset; set them instead")
			}
			s, ok := edit.Value.(string)
			if !ok || s == "" {
				return before, fmt.Errorf("%s must be a non-empty string", edit.Key)
			}
			rt := routes[key]
			switch field {
			case "provider":
				rt.Provider = s
			case "model":
				rt.Model = s
			case "effort":
				rt.Effort = s
				effortNamed[key] = true
			default:
				return before, fmt.Errorf("unsupported route field %q", field)
			}
			routes[key] = rt
			touched[key] = true
			continue
		}
		raw, _ := json.Marshal(edit.Value)
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			return before, err
		}
		switch edit.Key {
		case "/tick_seconds":
			before.TickSeconds = value
		case "/quiet_seconds":
			before.QuietSeconds = value
		default:
			return before, fmt.Errorf("unsupported runtime setting")
		}
	}
	// Validate the final picks of the routes this proposal touched, after
	// all edits are merged. Untouched routes pass through as they were.
	for key := range touched {
		rt := routes[key]
		label := routeNames[key]
		if rt.Model == "" {
			return before, fmt.Errorf("%s needs both a provider and a model", label)
		}
		if rt.Provider == "" {
			// An address no provider knows has no catalog to check the
			// pick against; the session has it or refuses it (running).
			continue
		}
		// The same rule as the project settings' (config.PickRoute with no
		// effort named): an effort the new model lacks becomes the nearest
		// one it offers. The pick then names the effort, so the session
		// sends the effort it reports (it applies the pick to its own
		// configured route, whose effort may differ).
		base, effort := config.ModelRoute{}, rt.Effort
		if !effortNamed[key] {
			base.ReasoningEffort, effort = rt.Effort, ""
		}
		picked, err := config.PickRoute(project, base, rt.Provider, rt.Model, effort)
		if err != nil {
			return before, err
		}
		rt.Effort = picked.ReasoningEffort
		routes[key] = rt
	}
	// The key is the one of the route the session runs the pick on (a
	// fallback's or a saved preset's own variable), not the provider's.
	if running != nil && len(touched) > 0 {
		run, err := running(routes)
		if err != nil {
			return before, err
		}
		for key := range touched {
			if a := run[key]; a.APIKeyEnv != "" && os.Getenv(a.APIKeyEnv) == "" {
				return before, fmt.Errorf("%s is not set where this session runs, so %s cannot use %s", a.APIKeyEnv, routeNames[key], cmp.Or(routes[key].Provider, a.BaseURL))
			}
		}
	}
	before.Routes = routes
	before.Relays = relays
	if savePreset != "" {
		before.Config = "bundle:" + savePreset
	}
	before.Revision++
	return before, nil
}

// SavePresetName returns the trimmed preset name when a proposal sets
// /save_preset, or "" otherwise.
func SavePresetName(p control.Proposal) string {
	for _, edit := range p.Edits {
		if edit.Key == "/save_preset" && edit.Op == "set" {
			s, _ := edit.Value.(string)
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// ConfigFromRoutes constructs a standalone Config representing a running
// session's current models and runtime settings so it can be saved as a
// reusable project preset. running holds the routes the session runs, as
// configured, by route key (Controller.Routes): those are saved as they
// are, whatever the session was started on and wherever the preset is
// saved. A route the session did not hand over (a controller with no
// Routes) is looked up in the project's own configuration, else picked
// from the catalog.
func ConfigFromRoutes(project string, values Values, running map[string]config.ModelRoute) (config.Config, error) {
	cfg, err := config.LoadBundle(project, "", "")
	if err != nil {
		cfg = config.Defaults()
	}
	cfg.Preset, cfg.Name, cfg.DefaultConfig, cfg.Instructions, cfg.Migrated = "", "", "", "", nil
	if values.TickSeconds > 0 {
		cfg.TickSeconds = values.TickSeconds
	}
	if values.QuietSeconds >= 0 {
		cfg.QuietSeconds = values.QuietSeconds
	}
	pick := func(key string, base config.ModelRoute) (config.ModelRoute, bool, error) {
		rt, ok := values.Routes[key]
		if !ok || rt.Model == "" {
			return base, false, nil
		}
		if r, ok := running[key]; ok {
			return r, true, nil
		}
		// A model the project's chain has (its fallback, say) is saved as
		// that entry is configured.
		if entry, ok := ChainEntry(project, base, rt); ok {
			base = entry
			if rt.Provider == "" {
				// At an address no provider knows there is no catalog to
				// pick from: the entry as it is, at the effort shown.
				base.ReasoningEffort = rt.Effort
				return base, true, nil
			}
		}
		if rt.Provider == "" {
			// Neither a catalog nor the project's chain has it: saving the
			// project's own model under the new name would not be what runs.
			return base, false, fmt.Errorf("%s: %s is at an address no provider is known for and is not in this project's configuration, so a preset cannot be built from it here; register its server (eagent servers add <url>) or save the preset in %s", routeNames[key], rt.Model, config.BundlesDir(project))
		}
		picked, err := config.PickRoute(project, base, rt.Provider, rt.Model, rt.Effort)
		if err != nil {
			return base, false, fmt.Errorf("%s: %w", routeNames[key], err)
		}
		if !llm.SameBaseURL(picked.BaseURL, base.BaseURL) || picked.Model != base.Model {
			picked.Fallback = nil
		}
		return picked, true, nil
	}
	summarizer := cfg.Summarizer.Route(cfg.Model)
	if cfg.Model, _, err = pick(RouteEagent, cfg.Model); err != nil {
		return cfg, err
	}
	picked, ok, err := pick(RouteSummarizer, summarizer)
	if err != nil {
		return cfg, err
	}
	if ok {
		cfg.Summarizer = config.Summarizer{Mode: config.SummarizerDedicated, Model: &picked}
	} else {
		// A session with no summarizer route keeps its summaries inline,
		// whatever config.json says now.
		cfg.Summarizer = config.Summarizer{Mode: config.SummarizerInline}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	if err := (&settings.Service{Project: project}).CheckRoutes(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
