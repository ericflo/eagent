package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Finding 26: a built-in preset chosen by name is that preset, whatever
// EAGENT_CONFIG names as the configuration in use or EAGENT_MODEL names as
// the model.
func TestChosenPresetIgnoresEnvConfigAndModel(t *testing.T) {
	unsetEnv(t, append(routeEnvNames(), "EAGENT_PRESET", "EAGENT_CONFIG")...)
	project := t.TempDir()
	if err := os.MkdirAll(BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	cheap := `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"OPENROUTER_API_KEY"}}`
	if err := os.WriteFile(filepath.Join(BundlesDir(project), "cheap.json"), []byte(cheap), 0o644); err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	Presets["anthropic-high"](&want)
	for name, env := range map[string][2]string{
		"EAGENT_CONFIG": {"EAGENT_CONFIG", "cheap"},
		"EAGENT_MODEL":  {"EAGENT_MODEL", "zai-org/GLM-5.3"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(env[0], env[1])
			got, err := ChosenPresetWithFile(project, "anthropic-high", map[string]json.RawMessage{})
			if err != nil {
				t.Fatalf("anthropic-high under %s=%s: %v", env[0], env[1], err)
			}
			if got.Model.Model != want.Model.Model || got.Model.BaseURL != want.Model.BaseURL {
				t.Fatalf("anthropic-high under %s=%s runs %s at %s", env[0], env[1], got.Model.Model, got.Model.BaseURL)
			}
		})
	}
}
