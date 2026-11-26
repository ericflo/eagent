package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/ericflo/eagent/internal/boundedfile"
	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// How the project's settings are laid out for a reader: sections, the cards
// related settings share, plain labels, and which are rarely changed.
type fieldPlace struct {
	section, group, label, description string
	advanced                           bool
}

var projectPlaces = map[string]fieldPlace{
	"/preset":                              {"Models", "Configuration", "Preset", "The built-in model new sessions start from. Choosing one sets the model to it.", false},
	"/default_config":                      {"Models", "Configuration", "Saved preset", "A preset saved in this project that new sessions start from instead.", false},
	"/summarizer/mode":                     {"Models", "Summarizer", "Title and summary", "How the thread's title and summary are kept.", false},
	"/relay/default":                       {"Behavior", "Agents", "Default agent", "The coding agent a session starts when it is not told which. Empty picks the first one installed.", false},
	"/persona":                             {"Behavior", "Voice", "Persona", "eagent's voice.", false},
	"/tick_seconds":                        {"Behavior", "Progress", "Seconds between progress looks", "How often eagent considers telling you how the work is going.", false},
	"/quiet_seconds":                       {"Behavior", "Progress", "Longest silence while the agent works", "In seconds: the longest you go without a progress line. 0 turns the limit off.", false},
	"/max_calls_per_turn":                  {"Behavior", "Work", "Calls per turn", "Model calls eagent may make each time it wakes: a look at the screen, a key press, another look.", true},
	"/rollover_tokens":                     {"Behavior", "Work", "Context rollover (tokens)", "At this prompt size eagent restarts its conversation from a dossier: where things stand, with a map of where everything is in the session's record. 0 follows the model: half its context window.", true},
	"/report_max_chars":                    {"Behavior", "Work", "Report kept (characters)", "How much of the agent's report eagent itself reads. You always get the whole report.", true},
	"/finalechat/enabled":                  {"FinaleChat", "Phone", "Mirror to the phone", "Off stops mirroring even with a token; on makes a missing token an error.", false},
	"/finalechat/mirror_input":             {"FinaleChat", "Phone", "Show terminal messages", "Copies what you type in the terminal or web UI into the thread.", false},
	"/finalechat/artifacts":                {"FinaleChat", "Phone", "Publish session archives", "Uploads each session's log and viewer to its thread.", false},
	"/finalechat/question_timeout_seconds": {"FinaleChat", "Phone", "Question timeout (seconds)", "How long a question to the phone stays open.", true},
	"/finalechat/agent":                    {"FinaleChat", "Phone", "Agent name", "Shown next to the thread in the app.", true},
	"/finalechat/base_url":                 {"FinaleChat", "Phone", "Server URL", "A self-hosted or staging FinaleChat.", true},
	"/finalechat/token_env":                {"FinaleChat", "Phone", "Token variable", "The environment variable holding the token.", true},
}

// SummarizerModeSuggestions say what each summarizer mode does, for the
// /summarizer/mode choice.
func SummarizerModeSuggestions() []control.Suggestion {
	return []control.Suggestion{
		{Value: config.SummarizerInline, Label: "Inline", Detail: "eagent keeps the title and summary itself, on the prompt it already has."},
		{Value: config.SummarizerDedicated, Label: "Dedicated", Detail: "A separate small call on its own model."},
	}
}

var routeLabels = map[string]string{
	"model":            "Model",
	"reasoning_effort": "Reasoning effort",
	"protocol":         "API protocol",
	"base_url":         "Base URL",
	"api_key_env":      "API key variable",
	"replay_reasoning": "Replay reasoning",
	"max_tokens":       "Max output tokens",
	"context_tokens":   "Context window (tokens)",
}

// placeProjectField gives a project settings field its section, card,
// label, and whether it is advanced. A route's model and effort are its
// card's main settings; the rest of a route, and every fallback route, are
// advanced.
func placeProjectField(f *control.Field) {
	if p, ok := projectPlaces[f.Key]; ok {
		f.Section, f.Group, f.Label, f.Advanced = p.section, p.group, p.label, p.advanced
		if p.description != "" && f.Description == "" {
			f.Description = p.description
		}
		return
	}
	parts := strings.Split(strings.TrimPrefix(f.Key, "/"), "/")
	var group string
	var route []string // the route's own keys: fallback/.../leaf
	switch {
	case len(parts) >= 2 && parts[0] == "model":
		// A reader puts the card's name before a label: "eagent model".
		group, route = "eagent", parts[1:]
	case len(parts) >= 3 && parts[0] == "summarizer" && parts[1] == "model":
		group, route = "Summarizer", parts[2:]
	default:
		f.Section, f.Advanced = "Other", true
		return
	}
	if depth := len(route) - 1; depth > 0 {
		group += " fallback"
		if depth > 1 {
			group += fmt.Sprintf(" %d", depth)
		}
	}
	leaf := route[len(route)-1]
	f.Section, f.Group = "Models", group
	if label, ok := routeLabels[leaf]; ok {
		f.Label = label
	}
	f.Advanced = len(route) > 1 || (leaf != "model" && leaf != "reasoning_effort")
	if leaf == "reasoning_effort" {
		// "" sends no effort, so the provider's default applies: the
		// editor's "default", which a preset's effort does not take away.
		f.Shape.Enum = []any{""}
		for _, e := range config.EffortOrder {
			f.Shape.Enum = append(f.Shape.Enum, e)
		}
	}
}

// ModelSuggestions are the models eagent knows of (registered servers' live
// models first, then the built-in catalog and cloud providers' live lists as
// last cached), grouped by provider id, for a client to offer as choices. Any
// model id can still be typed in.
func ModelSuggestions(project string) []control.Suggestion {
	return modelSuggestionsWithin(project, modelListBudget, func(pr config.Provider) string { return pr.ID })
}

// projectModelSuggestions are ModelSuggestions grouped by their provider's
// address, for the project settings, where a route names its provider by
// base_url: a model's group is the base_url picking it gives the route.
func projectModelSuggestions(project string, budget int) []control.Suggestion {
	return modelSuggestionsWithin(project, budget, func(pr config.Provider) string { return pr.BaseURL })
}

// providerAddressSuggestions name each address a route may use by its
// provider, for the project settings' base_url fields.
func providerAddressSuggestions(project string, addresses []any) []control.Suggestion {
	out := make([]control.Suggestion, 0, len(addresses))
	for _, v := range addresses {
		u, _ := v.(string)
		if u == "" || len(u) > 500 {
			continue
		}
		s := control.Suggestion{Value: u}
		if pr, ok := config.ProviderForURLFor(project, u); ok {
			s.Label = clip(pr.Label, 200)
		}
		s.Detail = clip(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"), 300)
		out = append(out, s)
	}
	return out
}

// modelListBudget is the share of a settings document (256 KiB) the model
// list may take, so a provider listing thousands of models cannot crowd
// out the settings themselves.
const modelListBudget = 160 << 10

// suggestionSize is roughly what a suggestion takes in a document.
func suggestionSize(s control.Suggestion) int {
	return len(s.Value) + len(s.Label) + len(s.Group) + len(s.Detail) + 48
}

// modelSuggestionsWithin is ModelSuggestions within budget bytes, each in the
// group group gives its provider.
func modelSuggestionsWithin(project string, budget int, group func(config.Provider) string) []control.Suggestion {
	models := config.KnownModels(project)
	providers := map[string]config.Provider{}
	for _, pr := range config.AllProviders(project) {
		providers[pr.ID] = pr
	}
	out := make([]control.Suggestion, 0, len(models))
	// Registered local models and the catalog come first.
	for _, m := range models {
		if m.ID == "" || len(m.ID) > 500 {
			continue
		}
		label := m.Label
		if label == "" || label == m.ID {
			label = ""
		}
		var detail []string
		if m.Price != nil {
			detail = append(detail, fmt.Sprintf("$%g / $%g per M", m.Price.In, m.Price.Out))
		} else if pr := providers[m.Provider]; pr.Custom {
			if pr.Engine != "" {
				detail = append(detail, pr.Engine)
			} else {
				detail = append(detail, "local")
			}
		}
		if m.Context > 0 {
			detail = append(detail, tokens(m.Context)+" context")
		}
		if m.Vision && providers[m.Provider].Custom {
			detail = append(detail, "vision")
		}
		pr, ok := providers[m.Provider]
		if !ok {
			pr = config.Provider{ID: m.Provider}
		}
		if group(pr) == "" {
			continue // a provider no longer known has no address to pick
		}
		sug := control.Suggestion{Value: m.ID, Label: clip(label, 200), Group: group(pr), Detail: clip(strings.Join(detail, " · "), 300)}
		if budget -= suggestionSize(sug); budget < 0 {
			break
		}
		out = append(out, sug)
	}
	return out
}

// ProviderSuggestions name the providers a route can use, including any
// project-registered local or custom servers.
func ProviderSuggestions(project string) []control.Suggestion {
	providers := config.AllProviders(project)
	out := make([]control.Suggestion, 0, len(providers))
	for _, pr := range providers {
		detail := ""
		if pr.Custom {
			detail = strings.TrimPrefix(strings.TrimPrefix(pr.BaseURL, "https://"), "http://")
		}
		out = append(out, control.Suggestion{Value: pr.ID, Label: clip(pr.Label, 200), Detail: clip(detail, 300)})
	}
	return out
}

// ConfigChoices are the configurations a session may run on: the built-in
// presets, then the ones saved in this project as bundle:NAME.
func ConfigChoices(project string) []string {
	out := append([]string{}, config.PresetNames()...)
	if bundles, err := config.ListBundles(project); err == nil {
		for _, b := range bundles {
			out = append(out, "bundle:"+b)
		}
	}
	return out
}

// SessionConfigSuggestions describe each configuration a running session may
// switch to as it would actually load in this project (its own settings on
// top), and the provider, model and effort it gives the session's routes,
// keyed /routes/eagent/... and, when its summarizer is dedicated,
// /routes/summarizer/..., so a reader can show what a choice brings.
func SessionConfigSuggestions(project string) []control.Suggestion {
	file := map[string]json.RawMessage{}
	if raw, err := readProjectFile(project); err == nil {
		file = raw
	}
	var out []control.Suggestion
	for _, name := range ConfigChoices(project) {
		preset, bundle := name, ""
		group := "Saved in this project"
		if b, ok := strings.CutPrefix(name, "bundle:"); ok {
			preset, bundle = "", b
		}
		s := control.Suggestion{Value: name}
		if bundle != "" {
			s.Label = bundle
		}
		var cfg config.Config
		var err error
		if bundle != "" {
			cfg, err = config.LoadBundleWithFile(project, "", bundle, file)
		} else {
			cfg, err = config.ChosenPresetWithFile(project, preset, file)
		}
		if err == nil {
			detail := lineup(project, cfg)
			if bundle != "" && cfg.Description != "" {
				detail += " — " + cfg.Description
			}
			s.Detail = clip(detail, 300)
			s.Implies = sessionImplies(project, cfg)
			if bundle == "" {
				group = "Presets"
				if pr, ok := config.ProviderForURLFor(project, cfg.Model.BaseURL); ok {
					group = pr.Label
				}
			}
		} else if bundle == "" {
			group = "Presets"
		}
		s.Group = group
		out = append(out, s)
	}
	return out
}

// BundleSuggestions describe each saved preset in this project with its model
// lineup (and optional description) for the /default_config picker. They omit
// Implies so /preset remains the sole governing field on Project defaults.
func BundleSuggestions(project string) []control.Suggestion {
	names, err := config.ListBundles(project)
	if err != nil || len(names) == 0 {
		return nil
	}
	out := make([]control.Suggestion, 0, len(names))
	for _, name := range names {
		s := control.Suggestion{Value: name, Label: name, Group: "Saved in this project"}
		cfg, err := config.BundleAlone(project, name)
		if err != nil {
			cfg, err = config.LoadBundle(project, "", name)
		}
		if err == nil {
			detail := lineup(project, cfg)
			if cfg.Description != "" {
				detail += " — " + cfg.Description
			}
			s.Detail = clip(detail, 300)
		}
		out = append(out, s)
	}
	return out
}

// PresetSuggestions describe each built-in preset as choosing it among the
// project's own settings gives it: the preset alone, since choosing one puts
// the project's model settings back to it. Each implies the values it gives
// those settings, by key.
func PresetSuggestions(project string) []control.Suggestion {
	var out []control.Suggestion
	for _, name := range config.PresetNames() {
		s := control.Suggestion{Value: name, Group: "Presets"}
		// The preset named as the bundle too: EAGENT_CONFIG does not replace it.
		if cfg, err := config.LoadBundleWithFile(project, name, name, map[string]json.RawMessage{}); err == nil {
			s.Detail = clip(lineup(project, cfg), 300)
			s.Implies = projectImplies(cfg)
			if pr, ok := config.ProviderForURLFor(project, cfg.Model.BaseURL); ok {
				s.Group = pr.Label
			}
		}
		out = append(out, s)
	}
	return out
}

// projectImplies maps a configuration's model route onto the project
// settings that hold it: its model, effort, and the route it is reached by.
func projectImplies(cfg config.Config) map[string]any {
	out := map[string]any{}
	a := cfg.Model
	for k, v := range map[string]string{"model": a.Model, "reasoning_effort": a.ReasoningEffort, "base_url": a.BaseURL, "protocol": a.Protocol, "api_key_env": a.APIKeyEnv} {
		if v != "" && len(v) <= 500 {
			out["/model/"+k] = v
		}
	}
	for k, v := range map[string]int{"max_tokens": a.MaxTokens, "context_tokens": a.ContextTokens} {
		if v > 0 {
			out["/model/"+k] = v
		}
	}
	if a.ReplayReasoning != nil {
		out["/model/replay_reasoning"] = *a.ReplayReasoning
	}
	return out
}

// lineup says which models a configuration runs: "GLM-5.3", or
// "GLM-5.3, summaries on GLM-5.3 Flash" with a dedicated summarizer.
func lineup(project string, cfg config.Config) string {
	name := func(a config.ModelRoute) string {
		if pr, ok := config.ProviderForURLFor(project, a.BaseURL); ok {
			if m, _, ok := config.ResolveLiveModel(project, pr.ID, a.Model); ok && m.Label != "" {
				return m.Label
			}
		}
		return a.Model
	}
	if cfg.Summarizer.Mode == config.SummarizerDedicated {
		return name(cfg.Model) + ", summaries on " + name(cfg.Summarizer.Route(cfg.Model))
	}
	return name(cfg.Model)
}

// sessionImplies maps a configuration onto a running session's route
// fields.
func sessionImplies(project string, cfg config.Config) map[string]any {
	routes := map[string]config.ModelRoute{"eagent": cfg.Model}
	if cfg.Summarizer.Mode == config.SummarizerDedicated {
		routes["summarizer"] = cfg.Summarizer.Route(cfg.Model)
	}
	out := map[string]any{}
	for name, a := range routes {
		for k, v := range routeInputs(project, a) {
			out["/routes/"+name+"/"+k] = v
		}
	}
	return out
}

// routeInputs are a route's provider, model and effort, each only when it
// is one a session's route fields accept.
func routeInputs(project string, a config.ModelRoute) map[string]string {
	out := map[string]string{}
	if pr, ok := config.ProviderForURLFor(project, a.BaseURL); ok {
		out["provider"] = pr.ID
	}
	if a.Model != "" && len(a.Model) <= 200 {
		out["model"] = a.Model
	}
	if slices.Contains(config.EffortOrder, a.ReasoningEffort) {
		out["effort"] = a.ReasoningEffort
	}
	return out
}

func readProjectFile(project string) (map[string]json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	raw, err := boundedfile.Read(config.File(project), 1<<20)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, err
	}
	return m, json.Unmarshal(raw, &m)
}

func tokens(n int) string {
	switch {
	case n >= 1_000_000 && n%1_000_000 == 0:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%dK", n/1000)
	}
	return fmt.Sprint(n)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
