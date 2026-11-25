package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// TestBrowserOwnSummarizerModelLeavesTheEnvironmentOut: "Give it its own
// model" copies eagent's model as the file and preset give it, not the
// values EAGENT_MODEL and its kin put in force, so a save does not write the
// environment's model into the summarizer's route. Runs by hand, like
// TestBrowserOldSession.
func TestBrowserOwnSummarizerModelLeavesTheEnvironmentOut(t *testing.T) {
	module := os.Getenv("EAGENT_PLAYWRIGHT_MODULE")
	if module == "" {
		t.Skip("set EAGENT_PLAYWRIGHT_MODULE to run the browser test")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	s, project := newTestServer(t)
	for k, v := range map[string]string{"EAGENT_MODEL": "claude-opus-5-5", "EAGENT_BASE_URL": "https://api.anthropic.com/v1", "EAGENT_PROTOCOL": "anthropic", "EAGENT_API_KEY_ENV": "ANTHROPIC_API_KEY"} {
		t.Setenv(k, v)
	}
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"summarizer":{"mode":"dedicated"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.loopbackOnly(s.Handler()))
	defer srv.Close()
	cmd := exec.Command("node", filepath.Join("testdata", "own-summarizer-browser.mjs"), srv.URL)
	cmd.Env = append(os.Environ(), "EAGENT_PLAYWRIGHT_MODULE="+module)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	raw, _ := os.ReadFile(config.File(project))
	if err != nil {
		t.Fatalf("browser test failed: %v\n%s", err, raw)
	}
	var file struct {
		Summarizer struct {
			Model *config.ModelRoute `json:"model"`
		} `json:"summarizer"`
	}
	_ = json.Unmarshal(raw, &file)
	if file.Summarizer.Model == nil || file.Summarizer.Model.Model != config.Defaults().Model.Model || file.Summarizer.Model.APIKeyEnv != config.Defaults().Model.APIKeyEnv || strings.Contains(strings.ToLower(string(raw)), "anthropic") {
		t.Fatalf("config.json = %s", raw)
	}
}
