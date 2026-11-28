package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/relay"
)

// EAGENT_PRESET names a saved preset whose own "preset" is openai-high, so
// loading takes it as the bundle and EAGENT_CONFIG (also openai-high) is
// not read at all. The refusal names what loading laid (the saved preset,
// and the variable that lays it), not a variable that happens to name the
// same preset.
func TestK19ServersUseNamesWhatLoadingLaid(t *testing.T) {
	relay.OfflineCatalogs()
	project := sr12Clean(t)
	t.Setenv("DEEPINFRA_API_KEY", "x")
	file := `{"finalechat":{"enabled":false}}`
	if err := os.WriteFile(config.File(project), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"preset":"openai-high","persona":"terse"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_PRESET", "mine")
	t.Setenv("EAGENT_CONFIG", "openai-high")
	cfg, err := config.Load(project, "")
	if err != nil || cfg.Name != "mine" || cfg.NameFrom != "EAGENT_PRESET" {
		t.Fatalf("loads %q from %q (%v)", cfg.Name, cfg.NameFrom, err)
	}
	var code int
	said := k18Stderr(t, func() {
		captureStdout(t, func() {
			code = cmdServersUse(context.Background(), project, []string{"deepinfra", "zai-org/GLM-5.3"}, serverCLIOpts{})
		})
	})
	raw, _ := os.ReadFile(config.File(project))
	if code == 0 || string(raw) != file || strings.Contains(said, "EAGENT_CONFIG") ||
		!strings.Contains(said, `the saved preset "mine", which EAGENT_PRESET lays over the project file here, names the preset openai-high`) ||
		!strings.Contains(said, "Unset EAGENT_PRESET first") {
		t.Fatalf("exit %d, told %q, file %s", code, said, raw)
	}
	// With the variable it named unset, nothing is laid, and the file is
	// written.
	os.Unsetenv("EAGENT_PRESET")
	os.Unsetenv("EAGENT_CONFIG")
	var out string
	captureStdout(t, func() {
		out = k18Stderr(t, func() {
			code = cmdServersUse(context.Background(), project, []string{"deepinfra", "zai-org/GLM-5.3"}, serverCLIOpts{})
		})
	})
	if now, err := config.Load(project, ""); err != nil || code != 0 || now.Model.BaseURL != "https://api.deepinfra.com/v1/openai" || now.Model.Model != "zai-org/GLM-5.3" {
		t.Fatalf("with it unset: exit %d (%v) %q; a session runs %s @ %s", code, err, out, now.Model.Model, now.Model.BaseURL)
	}
}

// `presets use` names the preset variable too, whatever else of the file the
// environment needs in order to load.
func TestK19PresetsUseNamesThePresetVariableWhateverElseTheFileNeeds(t *testing.T) {
	project := sr12Clean(t)
	file := `{"model":{"protocol":"openai-responses","base_url":"https://api.openai.com/v1","model":"gpt-6-luna","api_key_env":"OPENAI_API_KEY"},"relay":{"profiles":{"mine":{"agent":"codex"}}},"finalechat":{"enabled":false}}`
	if err := os.WriteFile(config.File(project), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_DEFAULT_RELAY", "mine")
	t.Setenv("EAGENT_PRESET", "openai-high")
	var code int
	said := k18Stderr(t, func() {
		captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "glm"}) })
	})
	raw, _ := os.ReadFile(config.File(project))
	if code == 0 || string(raw) != file || !strings.Contains(said, "EAGENT_PRESET names the preset openai-high here") {
		t.Fatalf("exit %d, said %q, file %s", code, said, raw)
	}
}

// `servers use` under EAGENT_PRESET naming another built-in, where the
// project file defines the relay profile EAGENT_DEFAULT_RELAY names (so
// the environment loads only together with the file): refused as it is
// without the relay default.
func TestK19ServersUseRefusesWhateverElseTheFileNeeds(t *testing.T) {
	relay.OfflineCatalogs()
	project := sr12Clean(t)
	t.Setenv("DEEPINFRA_API_KEY", "x")
	file := `{"relay":{"profiles":{"mine":{"agent":"codex"}}},"finalechat":{"enabled":false}}`
	if err := os.WriteFile(config.File(project), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_DEFAULT_RELAY", "mine")
	t.Setenv("EAGENT_PRESET", "openai-high")
	if _, err := config.Load(project, ""); err != nil {
		t.Fatalf("the project loads before: %v", err)
	}
	var code int
	var out string
	said := k18Stderr(t, func() {
		out = captureStdout(t, func() {
			code = cmdServersUse(context.Background(), project, []string{"deepinfra", "zai-org/GLM-5.3"}, serverCLIOpts{})
		})
	})
	raw, _ := os.ReadFile(config.File(project))
	cfg, _ := config.Load(project, "")
	t.Logf("exit %d\nout %q\nerr %q\nfile %s\nruns %s @ %s (%s)", code, out, said, raw, cfg.Model.Model, cfg.Model.BaseURL, cfg.Model.Protocol)
	if code == 0 || string(raw) != file || !strings.Contains(said, "EAGENT_PRESET names the preset openai-high here") {
		t.Fatalf("not refused as without the relay default: exit %d, file now %s; a session runs %s @ %s (%s)", code, raw, cfg.Model.Model, cfg.Model.BaseURL, cfg.Model.Protocol)
	}
}
