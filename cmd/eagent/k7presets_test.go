package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// `eagent presets save` saves what the files and the bundle say, never what
// only the environment gives (a persona, a rollover, FinaleChat turned off).
func TestK7PresetsSaveLeavesTheEnvironmentOut(t *testing.T) {
	r3NoEnv(t)
	project := t.TempDir()
	r3WriteConfig(t, project, `{"preset":"openai-high","persona":"concise","rollover_tokens":123456}`)
	t.Setenv("EAGENT_PERSONA", "pirate")
	t.Setenv("EAGENT_ROLLOVER_TOKENS", "99999")
	t.Setenv("EAGENT_FINALECHAT", "off")
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"save", "mine"}) })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	raw, err := os.ReadFile(config.BundlePath(project, "mine"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Persona        string `json:"persona"`
		RolloverTokens int    `json:"rollover_tokens"`
		Finalechat     struct {
			Enabled *bool `json:"enabled"`
		} `json:"finalechat"`
	}
	_ = json.Unmarshal(raw, &saved)
	if saved.Persona != "concise" || saved.RolloverTokens != 123456 || saved.Finalechat.Enabled != nil && !*saved.Finalechat.Enabled {
		t.Fatalf("saved persona %q rollover %d finalechat.enabled %v, want the file's concise and 123456 and FinaleChat not turned off\n%s", saved.Persona, saved.RolloverTokens, saved.Finalechat.Enabled, raw)
	}
}
