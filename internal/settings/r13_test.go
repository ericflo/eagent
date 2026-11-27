package settings

import (
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// A remote switch of preset runs the effort the preset's suggestion
// implies: a file whose route keeps only replay_reasoning or a null
// fallback once its model is unset is the preset's route as a whole, and a
// model picked in the same proposal carries the new preset's effort, not
// the old one's.
func TestARemotePresetSwitchRunsThePresetsEffort(t *testing.T) {
	implied := func(t *testing.T, project, preset string) (model, effort string) {
		for _, s := range PresetSuggestions(project) {
			if s.Value == preset {
				model, _ = s.Implies["/model/model"].(string)
				effort, _ = s.Implies["/model/reasoning_effort"].(string)
			}
		}
		if model == "" || effort == "" {
			t.Fatalf("%s implies no model or effort", preset)
		}
		return model, effort
	}
	for _, tc := range []struct {
		name, file, preset, pick string
	}{
		{"replay_reasoning", `{"model":{"model":"zai-org/GLM-5.3","replay_reasoning":false}}`, "openai-high", ""},
		{"null fallback", `{"model":{"model":"zai-org/GLM-5.3","fallback":null}}`, "openai-high", ""},
		{"replay_reasoning to qwen", `{"model":{"model":"zai-org/GLM-5.3","replay_reasoning":false}}`, "qwen", ""},
		{"model in the same proposal", `{}`, "openai-high", "gpt-6.1-sol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearRouteEnv(t)
			project := t.TempDir()
			writeProjectFile(t, project, tc.file)
			wantModel, wantEffort := implied(t, project, tc.preset)
			edits := []control.Edit{{Op: "set", Key: "/preset", Value: tc.preset}, {Op: "unset", Key: "/model/model"}}
			if tc.pick != "" {
				wantModel = tc.pick
				edits[1] = control.Edit{Op: "set", Key: "/model/model", Value: tc.pick}
			}
			s := &Service{Project: project}
			if err := tryApply(t, s, edits...); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Model.Model != wantModel || cfg.Model.ReasoningEffort != wantEffort {
				written, _ := os.ReadFile(config.File(project))
				t.Fatalf("got model=%s effort=%q, want %s @ %q\n%s", cfg.Model.Model, cfg.Model.ReasoningEffort, wantModel, wantEffort, written)
			}
		})
	}
}
