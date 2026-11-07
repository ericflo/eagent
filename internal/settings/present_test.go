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

// A new session's options say what leaving each alone means (the project's
// own lineup), and each configuration says what it would run here, file
// overrides included, so a reader can show it before anything starts.
func TestStartOptionsShowWhatEachChoiceRuns(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	// The project runs a preset with its own narrator on top.
	if err := os.WriteFile(config.File(project), []byte(`{"preset":"openai-med","narrator":{"reasoning_effort":"medium"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	action := startAction(t, view)
	inputs := map[string]control.Input{}
	for _, in := range action.Inputs {
		inputs[in.Name] = in
	}
	if inputs["config"].Default != "openai-med" || inputs["config"].Suggest == nil || inputs["config"].Suggest.List != "configs" {
		t.Fatalf("configuration input = %+v", inputs["config"])
	}
	own, err := config.LoadBundle(project, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for actor, a := range map[string]config.Actor{"orchestrator": own.Orchestrator, "task": own.Task, "narrator": own.Narrator} {
		model := inputs[actor+"_model"]
		if model.Group != actorName(actor) || model.Label != "Model" || model.Default != a.Model || model.Suggest.GroupBy != actor+"_provider" {
			t.Errorf("%s model input = %+v", actor, model)
		}
		if inputs[actor+"_effort"].Default != a.ReasoningEffort {
			t.Errorf("%s effort default = %v, want %s", actor, inputs[actor+"_effort"].Default, a.ReasoningEffort)
		}
		if pr, _ := config.ProviderForURL(a.BaseURL); inputs[actor+"_provider"].Default != pr.ID {
			t.Errorf("%s provider default = %v, want %s", actor, inputs[actor+"_provider"].Default, pr.ID)
		}
	}
	configs := view.Descriptor.Suggestions["configs"]
	if len(configs) != len(ConfigChoices(project)) {
		t.Fatalf("%d configuration suggestions for %d choices", len(configs), len(ConfigChoices(project)))
	}
	i := slices.IndexFunc(configs, func(s control.Suggestion) bool { return s.Value == "anthropic-high" })
	if i < 0 {
		t.Fatal("anthropic-high not suggested")
	}
	want, err := config.LoadBundle(project, "anthropic-high", "")
	if err != nil {
		t.Fatal(err)
	}
	got := configs[i]
	if got.Implies["orchestrator_model"] != want.Orchestrator.Model || got.Implies["task_model"] != want.Task.Model || got.Implies["orchestrator_provider"] != "anthropic" {
		t.Errorf("anthropic-high implies %v", got.Implies)
	}
	// The project's own narrator effort still applies on top of a preset.
	if got.Implies["narrator_effort"] != "medium" {
		t.Errorf("narrator effort under anthropic-high = %v, want the project's medium", got.Implies["narrator_effort"])
	}
	if got.Group != "Anthropic" || !strings.Contains(got.Detail, "orchestrating") {
		t.Errorf("anthropic-high suggestion = %+v", got)
	}
	for _, list := range []string{"providers", "models"} {
		if len(view.Descriptor.Suggestions[list]) == 0 {
			t.Errorf("no %s suggestions", list)
		}
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
		"/orchestrator/model":               {"Models", "Orchestrator", "Model", ""},
		"/task/reasoning_effort":            {"Models", "Task", "Reasoning effort", ""},
		"/task/base_url":                    {"Models", "Task", "Base URL", "advanced"},
		"/narrator/fallback/model":          {"Models", "Narrator fallback", "Model", "advanced"},
		"/narrator/fallback/fallback/model": {"Models", "Narrator fallback 2", "Model", "advanced"},
		"/task_concurrency":                 {"Behavior", "Work", "Tasks at once", ""},
		"/preset":                           {"Models", "Configuration", "Preset", ""},
		"/finalechat/base_url":              {"FinaleChat", "Phone", "Server URL", "advanced"},
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
	if !slices.Contains(fields["/task/reasoning_effort"].Shape.Enum, any("xhigh")) {
		t.Error("reasoning effort is not a choice")
	}
	if p := fields["/preset"]; p.Suggest == nil || p.Suggest.List != "presets" || slices.Contains(p.Shape.Enum, any("")) {
		t.Errorf("preset field = %+v", p)
	}
	// No saved configurations: nothing to choose, so nothing offered.
	if d := fields["/default_config"]; d.Writable || !d.Advanced {
		t.Errorf("default_config with no bundles = %+v", d)
	}
}

// However many models the providers list, the settings still publish: the
// model list is cut short before the document's cap.
func TestHugeModelListsStillPublish(t *testing.T) {
	project := t.TempDir()
	var models []config.Model
	for i := range 6000 {
		models = append(models, config.Model{Provider: "openrouter", ID: fmt.Sprintf("vendor-%04d/a-rather-long-model-name-for-testing-%d", i, i), Label: fmt.Sprintf("A Rather Long Model Name %d", i), Price: &config.Price{In: 1.25, Out: 10}, Context: 1_000_000, Live: true})
	}
	if err := config.WriteLiveCache(project, &config.LiveCache{FetchedAt: time.Now(), Models: models}); err != nil {
		t.Fatal(err)
	}
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view.Descriptor)
	if len(raw) > control.MaxDocumentBytes {
		t.Fatalf("descriptor is %d bytes", len(raw))
	}
	got := view.Descriptor.Suggestions["models"]
	if len(got) < 500 || len(got) >= len(models) {
		t.Fatalf("%d models offered of %d", len(got), len(models))
	}
	if got[0].Label == "" || !strings.Contains(got[0].Detail, "per M") {
		t.Errorf("first model = %+v", got[0])
	}
}

// Choosing a preset among the project's settings puts its models back to the
// preset's own, so each preset says what it gives those settings, by key.
func TestProjectPresetsSayWhatTheySet(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"preset":"openai-med","narrator":{"reasoning_effort":"medium"}}`), 0o644); err != nil {
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
	// The preset's own narrator effort, not the project's: choosing it resets that.
	if got.Implies["/orchestrator/model"] != "claude-fable-5-1" || got.Implies["/narrator/reasoning_effort"] != "low" || got.Implies["/task/api_key_env"] != "ANTHROPIC_API_KEY" || got.Group != "Anthropic" || !strings.Contains(got.Detail, "orchestrating") {
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
