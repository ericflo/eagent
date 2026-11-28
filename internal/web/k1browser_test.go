package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// "Give it its own model" under EAGENT_BASE_URL, which moves eagent's
// route to a registered 32k server: the summarizer's route is eagent's as
// the file and preset give it, so it is reached at the preset's address
// with that address's window, not with the window the variable's server
// gave (32768). Runs by hand, like TestBrowserOldSession.
func TestK1BrowserOwnSummarizerModelLeavesTheServersWindowOut(t *testing.T) {
	module := os.Getenv("EAGENT_PLAYWRIGHT_MODULE")
	if module == "" {
		t.Skip("set EAGENT_PLAYWRIGHT_MODULE to run the browser test")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	s, project := newTestServer(t)
	res, err := config.RegisterServer(context.Background(), project, config.ProbeOptions{URL: "http://127.0.0.1:9/v1", ID: "local", Label: "Local"}, true)
	if err != nil {
		t.Fatal(err)
	}
	config.UpdateLiveCacheForServer(project, "local", []config.Model{{Provider: "local", ID: "zai-org/GLM-5.3", Context: 32_768}}, "")
	t.Setenv("EAGENT_BASE_URL", res.Server.BaseURL)
	t.Setenv("EAGENT_API_KEY_ENV", "")
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"summarizer":{"mode":"dedicated"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.loopbackOnly(s.Handler()))
	defer srv.Close()
	cmd := exec.Command("node", filepath.Join("testdata", "k1-own-summarizer-browser.mjs"), srv.URL)
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
	want := config.Defaults().Model
	if m := file.Summarizer.Model; m == nil || m.BaseURL != want.BaseURL || m.ContextTokens == 32_768 {
		t.Fatalf("config.json = %s", raw)
	}
}
