package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// `eagent presets use openai-high` over a file that names a model says "the
// model the file named (...) was removed, so openai-high's model runs" and
// exits 0. With a saved preset in force from the environment (EAGENT_CONFIG),
// which lays its own model over the file, the next session runs that
// preset's model, not openai-high's.
func TestSR11PresetsUseSaysThePresetsModelRunsUnderEAGENTCONFIG(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("OPENAI_API_KEY", "x")
	t.Setenv("TOGETHER_API_KEY", "x")
	for _, n := range []string{"EAGENT_CONFIG", "EAGENT_PRESET", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_PROTOCOL", "EAGENT_API_KEY_ENV"} {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"},"finalechat":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_CONFIG", "mine")
	want := config.Defaults()
	config.Presets["openai-high"](&want)
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "openai-high"}) })
	t.Logf("exit %d\n%s", code, out)
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 && cfg.Model.Model != want.Model.Model {
		file, _ := os.ReadFile(config.File(project))
		t.Fatalf("presets use said %q and exited 0; a session now runs %s (bundle %q)\nfile: %s", out, cfg.Model.Model, cfg.Name, file)
	}
	// Refused, and the file's model is still there.
	if file, _ := os.ReadFile(config.File(project)); code == 0 || !strings.Contains(string(file), "zai-org/GLM-5.3") {
		t.Fatalf("exit %d, file %s", code, file)
	}
}

// The same line under EAGENT_MODEL, which names the model over every layer.
func TestSR11PresetsUseSaysThePresetsModelRunsUnderEAGENTMODEL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("OPENAI_API_KEY", "x")
	t.Setenv("TOGETHER_API_KEY", "x")
	for _, n := range []string{"EAGENT_CONFIG", "EAGENT_PRESET", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_PROTOCOL", "EAGENT_API_KEY_ENV"} {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"},"finalechat":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_MODEL", "zai-org/GLM-5.3")
	want := config.Defaults()
	config.Presets["openai-high"](&want)
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "openai-high"}) })
	t.Logf("exit %d\n%s", code, out)
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Logf("load after: %v", err)
	}
	if code == 0 && cfg.Model.Model != want.Model.Model {
		t.Fatalf("presets use said %q and exited 0; a session now runs %s @ %s (load error %v)", out, cfg.Model.Model, cfg.Model.BaseURL, err)
	}
	// Refused, and the route still holds: the file's address under the
	// variable's model.
	if code == 0 || cfg.Model.BaseURL != "https://api.together.xyz/v1" {
		t.Fatalf("exit %d, a session runs %s @ %s", code, cfg.Model.Model, cfg.Model.BaseURL)
	}
	// With the variable gone, the same command goes through.
	os.Unsetenv("EAGENT_MODEL")
	out = captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "openai-high"}) })
	if cfg, err = config.Load(project, ""); code != 0 || err != nil || cfg.Model.Model != want.Model.Model || !strings.Contains(out, "was removed") {
		t.Fatalf("without the variable: exit %d, %v, runs %s, said %q", code, err, cfg.Model.Model, out)
	}
}

// `eagent servers use PROVIDER MODEL` with a saved preset in force (the
// file's default_config) writes the file, as it always has; the saved
// preset lays its own model over the file, so the next session runs the
// preset's model. The command says that, and not "eagent -> MODEL".
func TestSR11ServersUseUnderASavedPresetInForceSaysWhatRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("OPENAI_API_KEY", "x")
	t.Setenv("TOGETHER_API_KEY", "x")
	for _, n := range []string{"EAGENT_CONFIG", "EAGENT_PRESET", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_PROTOCOL", "EAGENT_API_KEY_ENV"} {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
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
	t.Logf("exit %d\n%s", code, out)
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "eagent -> ") || !strings.Contains(out, `the saved preset "mine" is laid over the project file here`) || !strings.Contains(out, "the project's default_config lays it here") || !strings.Contains(out, "eagent runs "+cfg.Model.Model+" @ "+cfg.Model.BaseURL) {
		file, _ := os.ReadFile(config.File(project))
		t.Fatalf("servers use said %q (exit %d); a session now runs %s @ %s\nfile: %s", out, code, cfg.Model.Model, cfg.Model.BaseURL, file)
	}
}

// The file names only where the model is reached; a variable names the
// model. Choosing a preset would take the address away and leave the
// variable's model at the preset's: refused, and the file is left alone.
func TestK16PresetsUseLeavesAnAddressTheVariablesModelNeeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("OPENAI_API_KEY", "x")
	t.Setenv("LOCAL_KEY", "x")
	for _, n := range []string{"EAGENT_CONFIG", "EAGENT_PRESET", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_PROTOCOL", "EAGENT_API_KEY_ENV"} {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	const file = `{"model":{"base_url":"http://127.0.0.1:8080/v1","api_key_env":"LOCAL_KEY"},"finalechat":{"enabled":false}}`
	if err := os.WriteFile(config.File(project), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_MODEL", "my-local-model")
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "openai-high"}) })
	cfg, err := config.Load(project, "")
	if got, _ := os.ReadFile(config.File(project)); code == 0 || string(got) != file || err != nil || cfg.Model.BaseURL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("exit %d, said %q, load %v, runs %s @ %s\nfile: %s", code, out, err, cfg.Model.Model, cfg.Model.BaseURL, got)
	}
}
