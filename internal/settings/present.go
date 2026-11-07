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
	"/preset":                              {"Models", "Configuration", "Preset", "The built-in lineup of models new sessions start from. Choosing one sets every actor's models to it.", false},
	"/default_config":                      {"Models", "Configuration", "Saved configuration", "A configuration saved in this project that new sessions start from instead.", false},
	"/task_concurrency":                    {"Behavior", "Work", "Tasks at once", "How many task workers run at the same time.", false},
	"/max_task_turns":                      {"Behavior", "Work", "Turns per task", "Model calls a task may make before it is stopped.", false},
	"/max_orchestrator_calls_per_turn":     {"Behavior", "Work", "Orchestrator calls before a nudge", "After this many tool calls in a row the orchestrator is asked to delegate, wait, or yield.", true},
	"/rollover_tokens":                     {"Behavior", "Work", "Context rollover (tokens)", "At this prompt size the orchestrator starts fresh from a written summary.", true},
	"/persona":                             {"Behavior", "Narrator", "Persona", "The narrator's voice.", false},
	"/narrator_tick_seconds":               {"Behavior", "Narrator", "Check interval (seconds)", "How often the narrator looks in while work goes on.", false},
	"/narrator_quiet_seconds":              {"Behavior", "Narrator", "Quiet limit (seconds)", "The longest you go without a progress line while work continues. 0 turns the reminder off.", false},
	"/bash_wait_seconds":                   {"Behavior", "Commands", "Wait inline (seconds)", "How long a command runs before it continues in the background.", true},
	"/bash_timeout_seconds":                {"Behavior", "Commands", "Timeout (seconds)", "The default deadline for a command.", false},
	"/tool_output_max_chars":               {"Behavior", "Commands", "Output kept (characters)", "Longer tool output is cut to this.", true},
	"/allow_outside_project":               {"Behavior", "Commands", "Files outside the project", "Lets file tools read and write outside the project folder.", false},
	"/finalechat/enabled":                  {"FinaleChat", "Phone", "Mirror to the phone", "Off stops mirroring even with a token; on makes a missing token an error.", false},
	"/finalechat/mirror_input":             {"FinaleChat", "Phone", "Show terminal messages", "Copies what you type in the terminal or web UI into the thread.", false},
	"/finalechat/artifacts":                {"FinaleChat", "Phone", "Publish session archives", "Uploads each session's log and viewer to its thread.", false},
	"/finalechat/question_timeout_seconds": {"FinaleChat", "Phone", "Question timeout (seconds)", "How long a question to the phone stays open.", true},
	"/finalechat/agent":                    {"FinaleChat", "Phone", "Agent name", "Shown next to the thread in the app.", true},
	"/finalechat/base_url":                 {"FinaleChat", "Phone", "Server URL", "A self-hosted or staging FinaleChat.", true},
	"/finalechat/token_env":                {"FinaleChat", "Phone", "Token variable", "The environment variable holding the token.", true},
	"/relay/quiet_seconds":                 {"FinaleChat", "Relayed agents", "Quiet limit (seconds)", "The longest you go without a progress line while a relayed agent works.", true},
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
// label, and whether it is advanced. An actor's model and effort are its
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
	if len(parts) < 2 || !slices.Contains(StartActors, parts[0]) {
		f.Section, f.Advanced = "Other", true
		return
	}
	group := actorName(parts[0])
	if depth := len(parts) - 2; depth > 0 {
		group += " fallback"
		if depth > 1 {
			group += fmt.Sprintf(" %d", depth)
		}
	}
	leaf := parts[len(parts)-1]
	f.Section, f.Group = "Models", group
	if label, ok := routeLabels[leaf]; ok {
		f.Label = label
	}
	f.Advanced = len(parts) > 2 || (leaf != "model" && leaf != "reasoning_effort")
	if leaf == "reasoning_effort" {
		f.Shape.Enum = nil
		for _, e := range config.EffortOrder {
			f.Shape.Enum = append(f.Shape.Enum, e)
		}
	}
}

func actorName(actor string) string { return strings.ToUpper(actor[:1]) + actor[1:] }

// ModelSuggestions are the models eagent knows of (the built-in catalog and
// the providers' live lists as last cached), grouped by provider id, for a
// client to offer as choices. Any model id can still be typed in.
func ModelSuggestions(project string) []control.Suggestion {
	models := config.KnownModels(project)
	out := make([]control.Suggestion, 0, len(models))
	// A settings document is capped at 256 KiB; the list keeps to 160 KiB,
	// so a provider listing thousands of models cannot crowd out the
	// settings themselves. The catalog's models come first.
	budget := 160 << 10
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
		}
		if m.Context > 0 {
			detail = append(detail, tokens(m.Context)+" context")
		}
		sug := control.Suggestion{Value: m.ID, Label: clip(label, 200), Group: m.Provider, Detail: clip(strings.Join(detail, " · "), 300)}
		if budget -= len(sug.Value) + len(sug.Label) + len(sug.Group) + len(sug.Detail) + 48; budget < 0 {
			break
		}
		out = append(out, sug)
	}
	return out
}

// ProviderSuggestions name the providers a route can use.
func ProviderSuggestions() []control.Suggestion {
	out := make([]control.Suggestion, 0, len(config.Providers))
	for _, pr := range config.Providers {
		out = append(out, control.Suggestion{Value: pr.ID, Label: pr.Label})
	}
	return out
}

// ConfigChoices are the configurations a new session may start from: the
// built-in presets, then the ones saved in this project as bundle:NAME.
func ConfigChoices(project string) []string {
	out := append([]string{}, config.PresetNames()...)
	if bundles, err := config.ListBundles(project); err == nil {
		for _, b := range bundles {
			out = append(out, "bundle:"+b)
		}
	}
	return out
}

// ConfigSuggestions describe each configuration a session may start from as
// it would actually load in this project (its own settings on top), and the
// provider, model and effort it gives each actor, so a reader can show what
// a choice brings before anything starts.
func ConfigSuggestions(project string) []control.Suggestion {
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
		cfg, err := config.LoadBundleWithFile(project, preset, bundle, file)
		if err == nil {
			s.Detail = clip(lineup(project, cfg), 300)
			s.Implies = startImplies(cfg)
			if bundle == "" {
				group = "Presets"
				if pr, ok := config.ProviderForURL(cfg.Orchestrator.BaseURL); ok {
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

// PresetSuggestions describe each built-in preset as choosing it among the
// project's own settings gives it: the preset alone, since choosing one puts
// the project's model settings back to it. Each implies the values it gives
// those settings, by key.
func PresetSuggestions(project string) []control.Suggestion {
	var out []control.Suggestion
	for _, name := range config.PresetNames() {
		s := control.Suggestion{Value: name, Group: "Presets"}
		if cfg, err := config.LoadBundleWithFile(project, name, "", map[string]json.RawMessage{}); err == nil {
			s.Detail = clip(lineup(project, cfg), 300)
			s.Implies = projectImplies(cfg)
			if pr, ok := config.ProviderForURL(cfg.Orchestrator.BaseURL); ok {
				s.Group = pr.Label
			}
		}
		out = append(out, s)
	}
	return out
}

// projectImplies maps a configuration's routes onto the project settings
// that hold them: each actor's model, effort, and the route it is reached by.
func projectImplies(cfg config.Config) map[string]any {
	out := map[string]any{}
	for actor, a := range map[string]config.Actor{"orchestrator": cfg.Orchestrator, "task": cfg.Task, "narrator": cfg.Narrator} {
		p := "/" + actor + "/"
		for k, v := range map[string]string{"model": a.Model, "reasoning_effort": a.ReasoningEffort, "base_url": a.BaseURL, "protocol": a.Protocol, "api_key_env": a.APIKeyEnv} {
			if v != "" && len(v) <= 500 {
				out[p+k] = v
			}
		}
		for k, v := range map[string]int{"max_tokens": a.MaxTokens, "context_tokens": a.ContextTokens} {
			if v > 0 {
				out[p+k] = v
			}
		}
		if a.ReplayReasoning != nil {
			out[p+"replay_reasoning"] = *a.ReplayReasoning
		}
	}
	return out
}

// SessionConfigSuggestions are ConfigSuggestions for a running session's own
// settings, whose routes are keyed /routes/ACTOR/provider, model, effort.
func SessionConfigSuggestions(project string) []control.Suggestion {
	out := ConfigSuggestions(project)
	for i := range out {
		implied := map[string]any{}
		for k, v := range out[i].Implies {
			if actor, input, ok := strings.Cut(k, "_"); ok {
				implied["/routes/"+actor+"/"+input] = v
			}
		}
		out[i].Implies = implied
	}
	return out
}

// lineup says which models a configuration runs: "GLM-5.3 orchestrating,
// GLM-5.3 Flash working, DeepSeek V4.1 Flash narrating".
func lineup(project string, cfg config.Config) string {
	name := func(a config.Actor) string {
		if pr, ok := config.ProviderForURL(a.BaseURL); ok {
			if m, _, ok := config.ResolveLiveModel(project, pr.ID, a.Model); ok && m.Label != "" {
				return m.Label
			}
		}
		return a.Model
	}
	return fmt.Sprintf("%s orchestrating, %s working, %s narrating", name(cfg.Orchestrator), name(cfg.Task), name(cfg.Narrator))
}

// startImplies maps a configuration onto session.start's per-actor inputs.
func startImplies(cfg config.Config) map[string]any {
	out := map[string]any{}
	for actor, a := range map[string]config.Actor{"orchestrator": cfg.Orchestrator, "task": cfg.Task, "narrator": cfg.Narrator} {
		for k, v := range routeInputs(a) {
			out[actor+"_"+k] = v
		}
	}
	return out
}

// routeInputs are an actor route's provider, model and effort, each only
// when it is one session.start accepts.
func routeInputs(a config.Actor) map[string]string {
	out := map[string]string{}
	if pr, ok := config.ProviderForURL(a.BaseURL); ok {
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
