package runtimecontrol

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A preset saved from the settings of a session on its fallback is that
// fallback as configured: its key variable, window and reply ceiling, not
// the catalog's defaults for the model.
func TestAPresetSavedOnAFallbackIsTheFallbackAsConfigured(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL", "OPENROUTER_API_KEY"} {
		t.Setenv(n, "")
	}
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_KEY2", "y")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"EAGENT_TEST_KEY","context_tokens":200000,"max_tokens":32000,"reasoning_effort":"high",
		"fallback":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3-flash","api_key_env":"EAGENT_TEST_KEY2","context_tokens":50000,"max_tokens":4000,"reasoning_effort":"low"}}}`
	if err := os.WriteFile(config.File(project), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	values := Values{TickSeconds: 30, QuietSeconds: 180, Routes: map[string]Route{RouteEagent: {Provider: "openrouter", Model: "z-ai/glm-5.3-flash", Effort: "medium"}}}
	cfg, err := ConfigFromValues(project, values)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Model
	if m.Model != "z-ai/glm-5.3-flash" || m.APIKeyEnv != "EAGENT_TEST_KEY2" || m.ContextTokens != 50_000 || m.MaxTokens != 4_000 || m.ReasoningEffort != "medium" {
		t.Fatalf("saved %s key %s window %d reply %d @ %s", m.Model, m.APIKeyEnv, m.ContextTokens, m.MaxTokens, m.ReasoningEffort)
	}
}
