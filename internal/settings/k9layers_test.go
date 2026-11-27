package settings

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// CurrentLayers is the configuration the loader puts in force, whichever
// way it is chosen (EAGENT_PRESET, EAGENT_CONFIG or the file's
// default_config, naming a built-in preset or a saved bundle), without a
// value only the environment gives (EAGENT_ROLLOVER_TOKENS here).
func TestK9CurrentLayersIsTheConfigurationInForce(t *testing.T) {
	for _, row := range []struct {
		name, file, envName, envValue string
		model, persona                string
	}{
		{"EAGENT_PRESET built-in", `{"preset":"openai-high"}`, "EAGENT_PRESET", "anthropic-high", "claude-fable-5-1", ""},
		{"EAGENT_PRESET saved", `{"preset":"openai-high"}`, "EAGENT_PRESET", "work", "claude-opus-5-5", "terse"},
		{"EAGENT_CONFIG built-in", `{"preset":"openai-high"}`, "EAGENT_CONFIG", "anthropic-high", "claude-fable-5-1", ""},
		{"EAGENT_CONFIG saved", `{"preset":"openai-high"}`, "EAGENT_CONFIG", "work", "claude-opus-5-5", "terse"},
		{"default_config built-in", `{"preset":"openai-high","default_config":"anthropic-high"}`, "", "", "claude-fable-5-1", ""},
		{"default_config saved", `{"preset":"openai-high","default_config":"work"}`, "", "", "claude-opus-5-5", "terse"},
	} {
		t.Run(row.name, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", "")
			project := t.TempDir()
			writeProjectFile(t, project, row.file)
			work := config.Defaults()
			config.Presets["anthropic-med"](&work)
			work.Persona = "terse"
			if _, err := config.SaveBundle(project, "work", "", work); err != nil {
				t.Fatal(err)
			}
			if row.envName != "" {
				t.Setenv(row.envName, row.envValue)
			}
			t.Setenv("EAGENT_ROLLOVER_TOKENS", "99999")
			loaded, err := config.LoadBundle(project, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Model.Model != row.model || loaded.Persona != row.persona || loaded.RolloverTokens != 99999 {
				t.Fatalf("setup: in force %s persona %q rollover %d", loaded.Model.Model, loaded.Persona, loaded.RolloverTokens)
			}
			got, err := CurrentLayers(project, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Model.Model != row.model || got.Persona != row.persona || got.RolloverTokens == 99999 {
				t.Fatalf("CurrentLayers %s persona %q rollover %d; in force %s persona %q, and the rollover is only the environment's", got.Model.Model, got.Persona, got.RolloverTokens, row.model, row.persona)
			}
		})
	}
}
