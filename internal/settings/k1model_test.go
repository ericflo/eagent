package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// Finding 11 of the fifteenth review: a variable that names the model the
// route already has moves nothing (applyEnv keeps the window, Resolve shows
// it as the layers'), so a window the user enters is written and applies,
// not skipped as the variable's. A variable that names another model still
// owns the window: the one the user enters is not written.
func TestK1AWindowUnderAModelVariableIsTheVariablesOnlyWhereItMovedTheModel(t *testing.T) {
	const summarizer = `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}}`
	for _, c := range []struct {
		name, variable, model, file string
		window                      func(*config.Config) *int
		want                        int
	}{
		{"EAGENT_MODEL, the model in use", "EAGENT_MODEL", "zai-org/GLM-5.3", "", func(c *config.Config) *int { return &c.Model.ContextTokens }, 120_000},
		{"EAGENT_ORCHESTRATOR_MODEL, the model in use", "EAGENT_ORCHESTRATOR_MODEL", "zai-org/GLM-5.3", "", func(c *config.Config) *int { return &c.Model.ContextTokens }, 120_000},
		{"EAGENT_SUMMARIZER_MODEL, the model in use", "EAGENT_SUMMARIZER_MODEL", "zai-org/GLM-5.3", summarizer, func(c *config.Config) *int { return &c.Summarizer.Model.ContextTokens }, 120_000},
		{"EAGENT_MODEL, another model", "EAGENT_MODEL", "deepseek-ai/DeepSeek-V4.1-Flash-1017", "", func(c *config.Config) *int { return &c.Model.ContextTokens }, 1_048_576},
	} {
		t.Run(c.name, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv(c.variable, c.model)
			project := t.TempDir()
			if c.file != "" {
				k1WriteFile(t, project, c.file)
			}
			cfg := config.Resolve(project, "", "").Effective
			*c.window(&cfg) = 120_000
			raw, _ := json.Marshal(cfg)
			res, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw})
			if err != nil {
				t.Fatal(err)
			}
			if got := *c.window(&res.Resolution.Effective); got != c.want {
				t.Fatalf("window after save = %d (skipped %+v), want %d", got, res.Skipped, c.want)
			}
		})
	}
}
