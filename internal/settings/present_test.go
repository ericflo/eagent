package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

func startAction(t *testing.T, view RemoteView) control.Action {
	t.Helper()
	for _, a := range view.Descriptor.Actions {
		if a.Operation == "session.start" {
			return a
		}
	}
	t.Fatal("no session.start action")
	return control.Action{}
}

// A new session from the phone picks its first message, where it runs, and
// the agent, with that agent's model and effort asked only once it is
// picked. eagent's own model is not a start option.
func TestStartOptionsAreTheAgentsOwn(t *testing.T) {
	project := t.TempDir()
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	action := startAction(t, view)
	var params []string
	for name := range action.Parameters.Properties {
		if !strings.HasPrefix(name, "relay_") {
			params = append(params, name)
		}
	}
	slices.Sort(params)
	if !slices.Equal(params, []string{"cwd", "prompt", "relay"}) {
		t.Fatalf("parameters = %v", params)
	}
	relays := action.Parameters.Properties["relay"].Enum
	if slices.Contains(relays, any("")) || !slices.Contains(relays, any("claude")) || !slices.Contains(relays, any("codex")) {
		t.Fatalf("relay choices = %v", relays)
	}
	for _, in := range action.Inputs {
		profile, ok := strings.CutPrefix(in.Name, "relay_")
		if !ok {
			t.Errorf("input %s is not an agent's", in.Name)
			continue
		}
		profile = profile[:strings.LastIndex(profile, "_")]
		if when := in.When["relay"]; len(when) != 1 || when[0] != profile {
			t.Errorf("%s asked when %v", in.Name, in.When)
		}
		if _, ok := action.Parameters.Properties[in.Name]; !ok {
			t.Errorf("input %s has no parameter", in.Name)
		}
	}
	for name := range view.Descriptor.Suggestions {
		if name == "configs" || name == "providers" {
			t.Errorf("suggestion list %s has no reader in project settings", name)
		}
	}
}

// Each configuration a running session may switch to says what it would run
// here, file overrides included, keyed by the session's route fields.
func TestSessionConfigSuggestionsShowWhatEachRuns(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"preset":"openai-med","model":{"reasoning_effort":"medium"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	configs := SessionConfigSuggestions(project)
	if len(configs) != len(ConfigChoices(project)) {
		t.Fatalf("%d configuration suggestions for %d choices", len(configs), len(ConfigChoices(project)))
	}
	i := slices.IndexFunc(configs, func(s control.Suggestion) bool { return s.Value == "anthropic-high" })
	if i < 0 {
		t.Fatal("anthropic-high not suggested")
	}
	got := configs[i]
	if got.Implies["/routes/eagent/model"] != "claude-fable-5-1" || got.Implies["/routes/eagent/provider"] != "anthropic" {
		t.Errorf("anthropic-high implies %v", got.Implies)
	}
	// The project's own effort still applies on top of a preset.
	if got.Implies["/routes/eagent/effort"] != "medium" {
		t.Errorf("effort under anthropic-high = %v, want the project's medium", got.Implies["/routes/eagent/effort"])
	}
	if _, ok := got.Implies["/routes/summarizer/model"]; ok {
		t.Errorf("an inline summarizer implies a route: %v", got.Implies)
	}
	if got.Group != "Anthropic" || got.Detail != "Claude Fable 5.1" {
		t.Errorf("anthropic-high suggestion = %+v", got)
	}
	// With a dedicated summarizer, its route is implied too and named.
	if err := os.WriteFile(config.File(project), []byte(`{"summarizer":{"mode":"dedicated"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	configs = SessionConfigSuggestions(project)
	got = configs[slices.IndexFunc(configs, func(s control.Suggestion) bool { return s.Value == "glm" })]
	if got.Implies["/routes/summarizer/model"] != "zai-org/GLM-5.3" || !strings.Contains(got.Detail, "summaries on") {
		t.Errorf("dedicated glm = %+v", got)
	}
}

// The project's settings read as cards in sections, with plain labels; a
// route's plumbing and its fallbacks are advanced.
func TestProjectSettingsAreLaidOut(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]control.Field{}
	for _, f := range view.Descriptor.Fields {
		fields[f.Key] = f
		if f.Section == "" || strings.Contains(f.Label, "/") {
			t.Errorf("%s: section %q, label %q", f.Key, f.Section, f.Label)
		}
	}
	for key, want := range map[string][4]string{
		"/model/model":                   {"Models", "Model", "Model", ""},
		"/model/reasoning_effort":        {"Models", "Model", "Reasoning effort", ""},
		"/model/base_url":                {"Models", "Model", "Base URL", "advanced"},
		"/model/fallback/model":          {"Models", "Model fallback", "Model", "advanced"},
		"/model/fallback/fallback/model": {"Models", "Model fallback 2", "Model", "advanced"},
		"/summarizer/mode":               {"Models", "Summarizer", "Title and summary", ""},
		"/tick_seconds":                  {"Behavior", "Progress", "Seconds between progress looks", ""},
		"/quiet_seconds":                 {"Behavior", "Progress", "Longest silence while the agent works", ""},
		"/max_calls_per_turn":            {"Behavior", "Work", "Calls per turn", "advanced"},
		"/rollover_tokens":               {"Behavior", "Work", "Context rollover (tokens)", "advanced"},
		"/report_max_chars":              {"Behavior", "Work", "Report kept (characters)", "advanced"},
		"/persona":                       {"Behavior", "Voice", "Persona", ""},
		"/relay/default":                 {"Behavior", "Agents", "Default agent", ""},
		"/preset":                        {"Models", "Configuration", "Preset", ""},
		"/finalechat/base_url":           {"FinaleChat", "Phone", "Server URL", "advanced"},
	} {
		f, ok := fields[key]
		if !ok {
			t.Errorf("no %s field", key)
			continue
		}
		if got := [4]string{f.Section, f.Group, f.Label, map[bool]string{true: "advanced"}[f.Advanced]}; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if !slices.Contains(fields["/model/reasoning_effort"].Shape.Enum, any("xhigh")) {
		t.Error("reasoning effort is not a choice")
	}
	if m := fields["/summarizer/mode"]; !slices.Equal(m.Shape.Enum, []any{"inline", "dedicated"}) || m.Suggest == nil || len(view.Descriptor.Suggestions[m.Suggest.List]) != 2 {
		t.Errorf("summarizer mode = %+v", m)
	}
	for _, sug := range view.Descriptor.Suggestions["summarizer_modes"] {
		if sug.Detail == "" || sug.Label == "" {
			t.Errorf("summarizer mode %s is not explained", sug.Value)
		}
	}
	if r := fields["/relay/default"]; !slices.Contains(r.Shape.Enum, any("claude")) || slices.Contains(r.Shape.Enum, any("")) || !r.Unset {
		t.Errorf("relay default = %+v", r)
	}
	// An inline summarizer has no route to set, and nothing else is left of
	// settings that are gone.
	for key := range fields {
		if strings.HasPrefix(key, "/summarizer/model") || strings.HasPrefix(key, "/relay/profiles") || key == "/relay/quiet_seconds" {
			t.Errorf("unexpected field %s", key)
		}
	}
	for _, f := range view.Descriptor.Fields {
		for _, word := range []string{"orchestrat", "narrat", "task worker", "actor"} {
			if strings.Contains(strings.ToLower(f.Label+" "+f.Group+" "+f.Description), word) {
				t.Errorf("%s says %q: %s / %s / %s", f.Key, word, f.Group, f.Label, f.Description)
			}
		}
	}
	if p := fields["/preset"]; p.Suggest == nil || p.Suggest.List != "presets" || slices.Contains(p.Shape.Enum, any("")) {
		t.Errorf("preset field = %+v", p)
	}
	// No saved configurations: nothing to choose, so nothing offered.
	if d := fields["/default_config"]; d.Writable || !d.Advanced {
		t.Errorf("default_config with no bundles = %+v", d)
	}
}

// However many models the providers list, the list a session's settings
// offer is cut short well before a settings document's cap.
func TestHugeModelListsStillPublish(t *testing.T) {
	project := t.TempDir()
	var models []config.Model
	for i := range 6000 {
		models = append(models, config.Model{Provider: "openrouter", ID: fmt.Sprintf("vendor-%04d/a-rather-long-model-name-for-testing-%d", i, i), Label: fmt.Sprintf("A Rather Long Model Name %d", i), Price: &config.Price{In: 1.25, Out: 10}, Context: 1_000_000, Live: true})
	}
	if err := config.WriteLiveCache(project, &config.LiveCache{FetchedAt: time.Now(), Models: models}); err != nil {
		t.Fatal(err)
	}
	got := ModelSuggestions(project)
	raw, _ := json.Marshal(got)
	if len(raw) > control.MaxDocumentBytes*3/4 {
		t.Fatalf("model suggestions are %d bytes", len(raw))
	}
	if len(got) < 500 || len(got) >= len(models) {
		t.Fatalf("%d models offered of %d", len(got), len(models))
	}
	if got[0].Label == "" || !strings.Contains(got[0].Detail, "per M") {
		t.Errorf("first model = %+v", got[0])
	}
	s := &Service{Project: project}
	if _, err := s.RemoteSnapshot(s.Grant()); err != nil {
		t.Fatal(err)
	}
}

// Choosing a preset among the project's settings puts its models back to the
// preset's own, so each preset says what it gives those settings, by key.
func TestProjectPresetsSayWhatTheySet(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"preset":"openai-med","model":{"reasoning_effort":"medium"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]control.Field{}
	for _, f := range view.Descriptor.Fields {
		fields[f.Key] = f
	}
	if p := fields["/preset"]; p.Suggest == nil || p.Suggest.List != "presets" {
		t.Fatalf("preset field = %+v", p)
	}
	presets := view.Descriptor.Suggestions["presets"]
	if len(presets) != len(config.PresetNames()) {
		t.Fatalf("%d presets suggested", len(presets))
	}
	i := slices.IndexFunc(presets, func(s control.Suggestion) bool { return s.Value == "anthropic-high" })
	if i < 0 {
		t.Fatal("anthropic-high not suggested")
	}
	got := presets[i]
	// The preset's own effort, not the project's: choosing it resets that.
	if got.Implies["/model/model"] != "claude-fable-5-1" || got.Implies["/model/reasoning_effort"] != "low" || got.Implies["/model/api_key_env"] != "ANTHROPIC_API_KEY" || got.Group != "Anthropic" || got.Detail != "Claude Fable 5.1" {
		t.Fatalf("anthropic-high = %+v", got)
	}
	for _, p := range presets {
		for k := range p.Implies {
			if _, ok := fields[k]; !ok {
				t.Errorf("%s implies %s, which is no setting", p.Value, k)
			}
		}
	}
}
