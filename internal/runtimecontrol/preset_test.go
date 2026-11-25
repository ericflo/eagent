package runtimecontrol

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A session whose summaries are inline is saved as a preset with inline
// summaries, even after config.json was changed to a dedicated summarizer.
func TestConfigFromValuesKeepsTheSessionsSummarizer(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
	project := t.TempDir()
	values := FromConfigFor(project, config.Defaults())
	if _, ok := values.Routes[RouteSummarizer]; ok {
		t.Fatal("an inline session has a summarizer route")
	}
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3-Flash","api_key_env":"TOGETHER_API_KEY"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := ConfigFromValues(project, values)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Summarizer.Mode != config.SummarizerInline || cfg.Summarizer.Model != nil {
		t.Fatalf("summarizer = %+v", cfg.Summarizer)
	}
}
