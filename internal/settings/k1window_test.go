package settings

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// k1LocalGLM registers a keyless server whose list says it serves the
// defaults' zai-org/GLM-5.3 with a 32k window, and returns its address.
func k1LocalGLM(t *testing.T, project string) string {
	t.Helper()
	res, err := config.RegisterServer(context.Background(), project, config.ProbeOptions{URL: "http://127.0.0.1:9/v1", ID: "local", Label: "Local"}, true)
	if err != nil {
		t.Fatal(err)
	}
	config.UpdateLiveCacheForServer(project, "local", []config.Model{{Provider: "local", ID: "zai-org/GLM-5.3", Context: 32_768}}, "")
	return res.Server.BaseURL
}

func k1WriteFile(t *testing.T, project, raw string) {
	t.Helper()
	if err := os.MkdirAll(project+"/.agents/eagent", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// EAGENT_BASE_URL moves the route to a 32k server, and the file names the
// window: the window in force is the file's, the editor shows it as the
// file's, and a save of another window writes it.
func TestK1ASaveWritesTheWindowTheFileNamesUnderAMovingAddress(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	url := k1LocalGLM(t, project)
	t.Setenv("EAGENT_BASE_URL", url)
	t.Setenv("EAGENT_API_KEY_ENV", "")
	k1WriteFile(t, project, `{"model":{"context_tokens":16000}}`)
	before := config.Resolve(project, "", "")
	if got, src := before.Effective.Model.ContextTokens, before.Sources["/model/context_tokens"]; got != 16_000 || src != "file" {
		t.Fatalf("window before = %d from %q, want the file's 16000", got, src)
	}
	cfg := before.Effective
	cfg.Model.ContextTokens = 20_000
	raw, _ := json.Marshal(cfg)
	res, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Resolution.Effective.Model.ContextTokens; got != 20_000 {
		t.Fatalf("window after save = %d (skipped %+v, kept %+v), want 20000", got, res.Skipped, res.Kept)
	}
}

// EAGENT_SUMMARIZER_BASE_URL moves the summarizer route the file writes,
// with no window, to a 32k server: the window in force is that server's,
// shown as the variable's, and a save of something else does not write it
// to the file, where it would be the window of the file's own server once
// the variable is unset.
func TestK1ASaveUnderASummarizerAddressVariableDoesNotWriteItsWindow(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	url := k1LocalGLM(t, project)
	t.Setenv("EAGENT_SUMMARIZER_BASE_URL", url)
	t.Setenv("EAGENT_SUMMARIZER_API_KEY_ENV", "")
	k1WriteFile(t, project, `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}}`)
	before := config.Resolve(project, "", "")
	if s := before.Effective.Summarizer.Model; s == nil || s.ContextTokens != 32_768 || !strings.HasPrefix(before.Sources["/summarizer/model/context_tokens"], "env:") {
		t.Fatalf("summarizer before = %+v from %q", s, before.Sources["/summarizer/model/context_tokens"])
	}
	cfg := before.Effective
	cfg.TickSeconds = 15
	raw, _ := json.Marshal(cfg)
	if _, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw}); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(config.File(project))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), "context_tokens") || !strings.Contains(string(file), "tick_seconds") {
		t.Fatalf("file = %s", file)
	}
	os.Unsetenv("EAGENT_SUMMARIZER_BASE_URL")
	os.Unsetenv("EAGENT_SUMMARIZER_API_KEY_ENV")
	back, err := config.Load(project, "")
	if err != nil || back.Summarizer.Model == nil || back.Summarizer.Model.ContextTokens != 200_000 {
		t.Fatalf("without the variable: summarizer %+v err %v", back.Summarizer.Model, err)
	}
}
