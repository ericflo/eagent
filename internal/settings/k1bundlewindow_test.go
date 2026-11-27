package settings

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The file writes a summarizer route with a window of its own (12000); the
// bundle in use nulls it, so the summarizer the environment builds from
// eagent's route (EAGENT_SUMMARIZER_BASE_URL) runs at the variable's 32k
// server. A save of another field leaves the file's summarizer window as
// it is, not the window the environment's route shows.
func TestK1ASummarizerRouteTheEnvironmentBuildsOverABundleKeepsTheFilesWindow(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	url := k1LocalGLM(t, project)
	t.Setenv("EAGENT_SUMMARIZER_BASE_URL", url)
	t.Setenv("EAGENT_SUMMARIZER_API_KEY_ENV", "")
	k1WriteFile(t, project, `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY","context_tokens":12000}}}`)
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "b"), []byte(`{"summarizer":{"model":null}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{Project: project, Bundle: "b"}
	before := config.Resolve(project, "", "b")
	if m := before.Effective.Summarizer.Model; m == nil || m.ContextTokens != 32_768 {
		t.Fatalf("summarizer in force = %+v", m)
	}
	cfg := before.Effective
	cfg.TickSeconds = 15
	raw, _ := json.Marshal(cfg)
	if _, err := s.Save(context.Background(), Request{Config: raw}); err != nil {
		t.Fatal(err)
	}
	var file struct {
		Summarizer struct {
			Model *config.ModelRoute `json:"model"`
		} `json:"summarizer"`
	}
	written, _ := os.ReadFile(config.File(project))
	if err := json.Unmarshal(written, &file); err != nil || file.Summarizer.Model == nil || file.Summarizer.Model.ContextTokens != 12_000 {
		t.Fatalf("file = %s", written)
	}
}
