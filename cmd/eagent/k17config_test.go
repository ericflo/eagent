package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

func sr12Clean(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("OPENAI_API_KEY", "x")
	t.Setenv("TOGETHER_API_KEY", "x")
	for _, n := range []string{"EAGENT_CONFIG", "EAGENT_PRESET", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_PROTOCOL", "EAGENT_API_KEY_ENV",
		"EAGENT_ORCHESTRATOR_MODEL", "EAGENT_ORCHESTRATOR_BASE_URL", "EAGENT_ORCHESTRATOR_API_KEY_ENV", "EAGENT_ORCHESTRATOR_PROTOCOL"} {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	return project
}

// `eagent presets use openai-high` over a file that names Together's GLM,
// with EAGENT_BASE_URL (or EAGENT_API_KEY_ENV) pinning the route's address
// (or key variable) in eagent's environment. The check added in 5981b9b
// compares only the model name, so the command removes the file's route,
// exits 0 and says "openai-high's model runs"; a session now sends
// openai-high's model to Together's address (or Together's key to OpenAI).
func TestSR12PresetsUseUnderAPinnedAddressOrKey(t *testing.T) {
	for _, tc := range []struct{ env, value string }{
		{"EAGENT_BASE_URL", "https://api.together.xyz/v1"},
		{"EAGENT_API_KEY_ENV", "TOGETHER_API_KEY"},
	} {
		t.Run(tc.env, func(t *testing.T) {
			project := sr12Clean(t)
			if err := os.WriteFile(config.File(project), []byte(`{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"},"finalechat":{"enabled":false}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv(tc.env, tc.value)
			want := config.Defaults()
			config.Presets["openai-high"](&want)
			var code int
			out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "openai-high"}) })
			t.Logf("exit %d\n%s", code, out)
			cfg, err := config.Load(project, "")
			if err != nil {
				t.Logf("load after: %v", err)
			}
			if code == 0 && strings.Contains(out, "openai-high's model runs") &&
				(cfg.Model.BaseURL != want.Model.BaseURL || cfg.Model.APIKeyEnv != want.Model.APIKeyEnv) {
				t.Fatalf("presets use said %q and exited 0; a session now runs %s @ %s with key $%s (preset's: %s @ %s with $%s)",
					out, cfg.Model.Model, cfg.Model.BaseURL, cfg.Model.APIKeyEnv, want.Model.Model, want.Model.BaseURL, want.Model.APIKeyEnv)
			}
			// Refused: the file's route is as it was.
			if code == 0 || cfg.Model.Model != "zai-org/GLM-5.3" || cfg.Model.BaseURL != "https://api.together.xyz/v1" || cfg.Model.APIKeyEnv != "TOGETHER_API_KEY" {
				t.Fatalf("exit %d; a session runs %s @ %s with $%s", code, cfg.Model.Model, cfg.Model.BaseURL, cfg.Model.APIKeyEnv)
			}
		})
	}
}

// The same check, with EAGENT_CONFIG laying a saved preset that names
// glm's model id at DeepInfra: `presets use glm` removes the file's model and
// says "glm's model runs"; DeepInfra's route runs.
func TestSR12PresetsUseSameModelIdOtherProviderUnderEAGENTCONFIG(t *testing.T) {
	project := sr12Clean(t)
	t.Setenv("DEEPINFRA_API_KEY", "x")
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"model":{"protocol":"openai-chat","base_url":"https://api.deepinfra.com/v1/openai","model":"zai-org/GLM-5.3","api_key_env":"DEEPINFRA_API_KEY"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"model":{"protocol":"openai-responses","base_url":"https://api.openai.com/v1","model":"gpt-6-luna","api_key_env":"OPENAI_API_KEY"},"finalechat":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_CONFIG", "mine")
	want := config.Defaults()
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "glm"}) })
	t.Logf("exit %d\n%s", code, out)
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 && strings.Contains(out, "glm's model runs") && cfg.Model.BaseURL != want.Model.BaseURL {
		t.Fatalf("presets use said %q and exited 0; a session now runs %s @ %s (saved preset %q from %s), not glm's %s @ %s",
			out, cfg.Model.Model, cfg.Model.BaseURL, cfg.Name, cfg.NameFrom, want.Model.Model, want.Model.BaseURL)
	}
}

// `eagent servers use deepinfra zai-org/GLM-5.3` with a saved preset in force
// (default_config) that names the same model id at Together. The new check
// in servers.go compares only the model id, so the command prints
// "eagent -> zai-org/GLM-5.3 @ <DeepInfra>" while sessions run Together's.
func TestSR12ServersUseSameModelIdOtherProviderUnderSavedPreset(t *testing.T) {
	project := sr12Clean(t)
	t.Setenv("DEEPINFRA_API_KEY", "x")
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"default_config":"mine","finalechat":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStdout(t, func() {
		code = cmdServersUse(context.Background(), project, []string{"deepinfra", "zai-org/GLM-5.3"}, serverCLIOpts{})
	})
	t.Logf("exit %d\n%s", code, out)
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if i := strings.Index(out, "eagent -> "); i >= 0 && !strings.Contains(out[i:], cfg.Model.BaseURL) {
		t.Fatalf("servers use said %q; a session now runs %s @ %s (saved preset %q from %s)", out, cfg.Model.Model, cfg.Model.BaseURL, cfg.Name, cfg.NameFrom)
	}
}

// `eagent servers use` under a saved preset that EAGENT_CONFIG puts over the
// file says the model written "runs once another preset is chosen (eagent
// presets use NAME|default)". `presets use default` cannot change the
// variable: the saved preset still lays its own model.
func TestSR12ServersUseRemedyUnderEAGENTCONFIG(t *testing.T) {
	project := sr12Clean(t)
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"finalechat":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_CONFIG", "mine")
	want := config.Defaults()
	config.Presets["openai-high"](&want)
	pr, ok := config.ProviderForURL(want.Model.BaseURL)
	if !ok {
		t.Fatal("no provider for openai-high")
	}
	var code int
	out := captureStdout(t, func() {
		code = cmdServersUse(context.Background(), project, []string{pr.ID, want.Model.Model}, serverCLIOpts{})
	})
	t.Logf("servers use: exit %d\n%s", code, out)
	// The remedy it names is the variable's, and it works.
	if strings.Contains(out, "eagent -> ") || !strings.Contains(out, "EAGENT_CONFIG lays it here: unset it") || strings.Contains(out, "presets use") {
		t.Fatalf("servers use said %q", out)
	}
	os.Unsetenv("EAGENT_CONFIG")
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Model != want.Model.Model {
		t.Fatalf("servers use said %q; after the remedy it names a session still runs %s (saved preset %q from %s)", out, cfg.Model.Model, cfg.Name, cfg.NameFrom)
	}
}

// Under the project's own default_config the remedy is to clear it, and
// that works too.
func TestK17ServersUseRemedyUnderDefaultConfig(t *testing.T) {
	project := sr12Clean(t)
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"default_config":"mine","finalechat":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	want := config.Defaults()
	config.Presets["openai-high"](&want)
	pr, ok := config.ProviderForURL(want.Model.BaseURL)
	if !ok {
		t.Fatal("no provider for openai-high")
	}
	var code int
	out := captureStdout(t, func() {
		code = cmdServersUse(context.Background(), project, []string{pr.ID, want.Model.Model}, serverCLIOpts{})
	})
	if strings.Contains(out, "eagent -> ") || !strings.Contains(out, "`eagent presets use default` clears that") {
		t.Fatalf("servers use said %q (exit %d)", out, code)
	}
	captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "default"}) })
	cfg, err := config.Load(project, "")
	if err != nil || code != 0 || cfg.Model.Model != want.Model.Model {
		t.Fatalf("after `presets use default` (exit %d, %v) a session runs %s (saved preset %q)", code, err, cfg.Model.Model, cfg.Name)
	}
}
