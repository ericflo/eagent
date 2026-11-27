package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/relay"
)

// `eagent servers use deepinfra zai-org/GLM-5.3` with EAGENT_PRESET (or
// EAGENT_CONFIG) naming a built-in preset. The save writes the route as a
// difference from the file's own base (the defaults: Together's GLM-5.3), so
// only the address and key variable land in the file; loading lays them over
// the variable's preset, whose model and protocol stay. The command prints
// "eagent -> zai-org/GLM-5.3 @ <DeepInfra>" (no saved preset is laid, so the
// new check is skipped) while a session runs another model at DeepInfra.
func TestSR13ServersUseUnderBuiltinPresetVariable(t *testing.T) {
	relay.OfflineCatalogs()
	for _, env := range []string{"EAGENT_PRESET", "EAGENT_CONFIG"} {
		t.Run(env, func(t *testing.T) {
			project := sr12Clean(t)
			t.Setenv("DEEPINFRA_API_KEY", "x")
			if err := os.WriteFile(config.File(project), []byte(`{"finalechat":{"enabled":false}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv(env, "openai-high")
			var code int
			out := captureStdout(t, func() {
				code = cmdServersUse(context.Background(), project, []string{"deepinfra", "zai-org/GLM-5.3"}, serverCLIOpts{})
			})
			raw, _ := os.ReadFile(config.File(project))
			t.Logf("exit %d\n%s\nfile: %s", code, out, raw)
			cfg, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			// Refused, naming the variable, and the file is as it was.
			if code == 0 || string(raw) != `{"finalechat":{"enabled":false}}` {
				t.Fatalf("exit %d; file %s", code, raw)
			}
			i := strings.Index(out, "eagent -> ")
			if code == 0 && i >= 0 && (!strings.Contains(out[i:], cfg.Model.Model+" @ ") || !strings.Contains(out[i:], cfg.Model.BaseURL) || !strings.Contains(out[i:], "("+cfg.Model.Protocol+")")) {
				t.Fatalf("servers use said %q; a session now runs %s @ %s (%s) with $%s", out[i:], cfg.Model.Model, cfg.Model.BaseURL, cfg.Model.Protocol, cfg.Model.APIKeyEnv)
			}
		})
	}
}

// k18Stderr is what fn writes to standard error.
func k18Stderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stderr = old
	w.Close()
	return <-done
}

// `eagent presets use glm` over a file that names a route, with
// EAGENT_CONFIG naming another built-in preset: that preset goes in place
// of the file's, so glm would not run. Refused, naming the variable, and
// the file is left as it was.
func TestK18PresetsUseUnderAnotherBuiltInNamesTheVariable(t *testing.T) {
	project := sr12Clean(t)
	file := `{"model":{"protocol":"openai-responses","base_url":"https://api.openai.com/v1","model":"gpt-6-luna","api_key_env":"OPENAI_API_KEY"},"finalechat":{"enabled":false}}`
	if err := os.WriteFile(config.File(project), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_CONFIG", "openai-high")
	var code int
	said := k18Stderr(t, func() {
		captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "glm"}) })
	})
	raw, _ := os.ReadFile(config.File(project))
	if code == 0 || string(raw) != file || !strings.Contains(said, "EAGENT_CONFIG names the preset openai-high here") {
		t.Fatalf("exit %d, said %q, file %s", code, said, raw)
	}
}

// `eagent servers use` under EAGENT_PRESET naming a built-in other than the
// file's own says which variable stands in the way, also where the file
// names a preset of its own (a loaded configuration then carries the
// file's name, whatever was laid). The same preset in both is no obstacle.
func TestK18ServersUseUnderAPresetVariableNamesIt(t *testing.T) {
	relay.OfflineCatalogs()
	for _, c := range []struct {
		file    string
		refused bool
	}{
		{`{"finalechat":{"enabled":false}}`, true},
		{`{"preset":"glm","finalechat":{"enabled":false}}`, true},
		{`{"preset":"openai-high","finalechat":{"enabled":false}}`, false},
	} {
		project := sr12Clean(t)
		t.Setenv("DEEPINFRA_API_KEY", "x")
		if err := os.WriteFile(config.File(project), []byte(c.file), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("EAGENT_PRESET", "openai-high")
		var code int
		var out string
		said := k18Stderr(t, func() {
			out = captureStdout(t, func() {
				code = cmdServersUse(context.Background(), project, []string{"deepinfra", "zai-org/GLM-5.3"}, serverCLIOpts{})
			})
		})
		raw, _ := os.ReadFile(config.File(project))
		if c.refused {
			if code == 0 || string(raw) != c.file || !strings.Contains(said, "EAGENT_PRESET names the preset openai-high here") {
				t.Fatalf("%s: exit %d, said %q, file %s", c.file, code, said, raw)
			}
			continue
		}
		cfg, err := config.Load(project, "")
		if err != nil || code != 0 || !strings.Contains(out, "eagent -> "+cfg.Model.Model+" @ "+cfg.Model.BaseURL) || cfg.Model.Model != "zai-org/GLM-5.3" {
			t.Fatalf("%s: exit %d (%v), said %q %q; a session runs %s @ %s", c.file, code, err, out, said, cfg.Model.Model, cfg.Model.BaseURL)
		}
	}
}
