package settings

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// Finding 13 of the fifteenth review: a bundle given as JSON is laid over
// the preset it names as the loader lays it (overlay, followModel): a
// route it moves to another model, or to another server for the same
// model, without naming a window gets the window there, not the preset's;
// one it names stays; its null follows the model. The bundle saved from it
// loads with that window.
func TestK1ABundleGivenAsJSONTakesTheWindowWhereItsRouteIs(t *testing.T) {
	clearRouteEnv(t)
	for _, c := range []struct {
		name, raw string
		want      int
	}{
		{"another model", `{"preset":"glm","model":{"model":"deepseek-ai/DeepSeek-V4.1-Flash-1017"}}`, 1_048_576},
		{"another server", `{"preset":"glm","model":{"base_url":"http://127.0.0.1:9/v1","api_key_env":""}}`, 32_768},
		{"a window it names", `{"preset":"glm","model":{"base_url":"http://127.0.0.1:9/v1","api_key_env":"","context_tokens":16000}}`, 16_000},
		{"the editor's null", `{"preset":"glm","model":{"model":"deepseek-ai/DeepSeek-V4.1-Flash-1017","context_tokens":null}}`, 1_048_576},
		{"the preset's model", `{"preset":"glm","model":{"reasoning_effort":"low"}}`, 200_000},
	} {
		t.Run(c.name, func(t *testing.T) {
			project := t.TempDir()
			k1LocalGLM(t, project)
			s := &Service{Project: project}
			cfg, err := s.BundleConfig(BundleRequest{Name: "mine", Config: json.RawMessage(c.raw)})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Model.ContextTokens != c.want {
				t.Fatalf("window = %d, want %d", cfg.Model.ContextTokens, c.want)
			}
			if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := config.SaveBundle(project, "mine", "", cfg); err != nil {
				t.Fatal(err)
			}
			loaded, err := config.LoadBundle(project, "", "mine")
			if err != nil || loaded.Model.ContextTokens != c.want {
				t.Fatalf("saved bundle loads with window %d (err %v), want %d", loaded.Model.ContextTokens, err, c.want)
			}
		})
	}
}
