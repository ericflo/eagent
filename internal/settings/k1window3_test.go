package settings

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The reader's cases for 0963480 (kr-wire rv15_window_test.go). The stale
// form's request says what the form was loaded with, as the editor sends it
// (config.js save: loaded); the rest are as the reader wrote them.

func rv15b(t *testing.T) (project, url string) {
	t.Helper()
	clearRouteEnv(t)
	project = t.TempDir()
	url = k1LocalGLM(t, project)
	t.Setenv("EAGENT_BASE_URL", url)
	t.Setenv("EAGENT_API_KEY_ENV", "")
	return project, url
}

func rv15bSave(t *testing.T, project string, req Request) Result {
	t.Helper()
	res, err := (&Service{Project: project}).Save(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func rv15bUnsetLoad(t *testing.T, project string) config.Config {
	t.Helper()
	os.Unsetenv("EAGENT_BASE_URL")
	os.Unsetenv("EAGENT_API_KEY_ENV")
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A save of another field under the variable writes no window, in either
// mode, and the window is the preset's once the variable is unset.
func TestRV15BASaveOfAnotherFieldWritesNoWindow(t *testing.T) {
	for _, mode := range []string{"", "pin"} {
		t.Run("mode="+mode, func(t *testing.T) {
			project, _ := rv15b(t)
			cfg := config.Resolve(project, "", "").Effective
			cfg.TickSeconds = 15
			raw, _ := json.Marshal(cfg)
			rv15bSave(t, project, Request{Config: raw, Mode: mode})
			file, _ := os.ReadFile(config.File(project))
			if strings.Contains(string(file), "context_tokens") {
				t.Fatalf("file = %s", file)
			}
			if got := rv15bUnsetLoad(t, project).Model.ContextTokens; got != 200_000 {
				t.Fatalf("without the variable: window %d", got)
			}
		})
	}
}

// The form was loaded while the server's list said 32k; the list is
// refreshed (64k) before the user saves another field. The user entered no
// window, so none should be written. The form says what it was loaded
// with, as the editor does.
func TestRV15BAStaleFormWritesNoWindow(t *testing.T) {
	project, _ := rv15b(t)
	cfg := config.Resolve(project, "", "").Effective
	loaded, _ := json.Marshal(cfg)
	config.UpdateLiveCacheForServer(project, "local", []config.Model{{Provider: "local", ID: "zai-org/GLM-5.3", Context: 65_536}}, "")
	cfg.TickSeconds = 15
	raw, _ := json.Marshal(cfg)
	res := rv15bSave(t, project, Request{Config: raw, Loaded: loaded})
	file, _ := os.ReadFile(config.File(project))
	if strings.Contains(string(file), "context_tokens") {
		t.Fatalf("window in force after save %d; file = %s; without the variable: %d", res.Resolution.Effective.Model.ContextTokens, file, rv15bUnsetLoad(t, project).Model.ContextTokens)
	}
}

// The same stale form with a window the user did enter: it is written.
func TestK1AStaleFormWritesTheWindowTheUserEntered(t *testing.T) {
	project, _ := rv15b(t)
	cfg := config.Resolve(project, "", "").Effective
	loaded, _ := json.Marshal(cfg)
	config.UpdateLiveCacheForServer(project, "local", []config.Model{{Provider: "local", ID: "zai-org/GLM-5.3", Context: 65_536}}, "")
	cfg.Model.ContextTokens = 16_000
	raw, _ := json.Marshal(cfg)
	if got := rv15bSave(t, project, Request{Config: raw, Loaded: loaded}).Resolution.Effective.Model.ContextTokens; got != 16_000 {
		t.Fatalf("window after save = %d, want 16000", got)
	}
}

// A window the file names, cleared in the editor: the window follows the
// server again under the variable, and the preset's without it.
func TestRV15BAClearedWindowFollowsAgain(t *testing.T) {
	project, _ := rv15b(t)
	k1WriteFile(t, project, `{"model":{"context_tokens":16000}}`)
	cfg := config.Resolve(project, "", "").Effective
	loaded, _ := json.Marshal(cfg) // as the editor sends it
	cfg.Model.ContextTokens = 0
	raw, _ := json.Marshal(cfg)
	res := rv15bSave(t, project, Request{Config: raw, Loaded: loaded})
	if got := res.Resolution.Effective.Model.ContextTokens; got != 32_768 {
		file, _ := os.ReadFile(config.File(project))
		t.Fatalf("window after clearing = %d; file %s", got, file)
	}
	// Without the variable the editor's null leaves 0 (read as 200k), as at
	// f36e19c: not checked here.
}

// The summarizer's own route in the file, under EAGENT_SUMMARIZER_BASE_URL:
// a save of another summarizer field (its effort) writes no window.
func TestRV15BASummarizerEffortSaveWritesNoWindow(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	url := k1LocalGLM(t, project)
	t.Setenv("EAGENT_SUMMARIZER_BASE_URL", url)
	t.Setenv("EAGENT_SUMMARIZER_API_KEY_ENV", "")
	k1WriteFile(t, project, `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}}`)
	before := config.Resolve(project, "", "")
	if src := before.Sources["/summarizer/model/context_tokens"]; src != "env" {
		t.Logf("summarizer window source %q", src)
	}
	cfg := before.Effective
	cfg.Summarizer.Model.ReasoningEffort = "low"
	cfg.Summarizer.Model.MaxTokens = 4000
	raw, _ := json.Marshal(cfg)
	rv15bSave(t, project, Request{Config: raw})
	file, _ := os.ReadFile(config.File(project))
	if strings.Contains(string(file), "context_tokens") {
		t.Fatalf("file = %s", file)
	}
}

// Under EAGENT_BASE_URL moving the route to a server that serves the model
// with the window the route had (200k), a save of another field writes no
// window, though Overlay writes the window shown for a route moved to
// another address (finding 10): the move is the environment's, not the
// file's, and the user entered none.
func TestK1AnAddressVariablesMoveDoesNotPinTheWindow(t *testing.T) {
	project, _ := rv15b(t)
	config.UpdateLiveCacheForServer(project, "local", []config.Model{{Provider: "local", ID: "zai-org/GLM-5.3", Context: 200_000}}, "")
	cfg := config.Resolve(project, "", "").Effective
	cfg.TickSeconds = 15
	raw, _ := json.Marshal(cfg)
	rv15bSave(t, project, Request{Config: raw})
	file, _ := os.ReadFile(config.File(project))
	if strings.Contains(string(file), "context_tokens") {
		t.Fatalf("file = %s", file)
	}
}
