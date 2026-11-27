package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// `eagent presets save copy` in a project whose config.json makes a saved
// preset the default (`eagent presets use work` writes default_config):
// `eagent presets list` marks work as in force and `eagent config` shows
// work's model, and on main the copy held work's settings (LoadBundle
// follows default_config). config.Resolve does not follow default_config,
// so the copy now holds the file's preset and none of work's settings.
func TestSR3PresetsSaveKeepsTheDefaultSavedPreset(t *testing.T) {
	for _, viaEnv := range []bool{false, true} {
		name := "default_config"
		if viaEnv {
			name = "EAGENT_CONFIG"
		}
		t.Run(name, func(t *testing.T) { sr3PresetsSaveKeeps(t, viaEnv) })
	}
}

func sr3PresetsSaveKeeps(t *testing.T, viaEnv bool) {
	r3NoEnv(t)
	project := t.TempDir()
	if viaEnv {
		r3WriteConfig(t, project, `{"preset":"openai-high"}`)
		t.Setenv("EAGENT_CONFIG", "work")
	} else {
		r3WriteConfig(t, project, `{"preset":"openai-high","default_config":"work"}`)
	}
	work := config.Defaults()
	config.Presets["anthropic-med"](&work)
	work.Persona = "terse"
	work.RolloverTokens = 77777
	if _, err := config.SaveBundle(project, "work", "my work setup", work); err != nil {
		t.Fatal(err)
	}
	inForce, err := config.LoadBundle(project, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if inForce.Name != "work" || inForce.Model.Model != "claude-opus-5-5" {
		t.Fatalf("setup: in force %q model %q, want bundle work on claude-opus-5-5", inForce.Name, inForce.Model.Model)
	}
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"save", "copy"}) })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	saved, err := config.LoadBundle(project, "", "copy")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Model.Model != inForce.Model.Model || saved.Persona != "terse" || saved.RolloverTokens != 77777 {
		t.Fatalf("saved copy runs model %q persona %q rollover %d; the configuration in force (bundle work) runs %q persona %q rollover %d",
			saved.Model.Model, saved.Persona, saved.RolloverTokens, inForce.Model.Model, inForce.Persona, inForce.RolloverTokens)
	}
}

// `eagent config --write` writes a new config.json from what the files say,
// never what only the environment gives (FinaleChat turned off, a persona).
func TestK8ConfigWriteLeavesTheEnvironmentOut(t *testing.T) {
	r3NoEnv(t)
	project := t.TempDir()
	t.Setenv("EAGENT_FINALECHAT", "off")
	t.Setenv("EAGENT_PERSONA", "pirate")
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", true, nil) })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	raw, err := os.ReadFile(config.File(project))
	if err != nil {
		t.Fatal(err)
	}
	var written struct {
		Persona    string `json:"persona"`
		Finalechat struct {
			Enabled *bool `json:"enabled"`
		} `json:"finalechat"`
	}
	_ = json.Unmarshal(raw, &written)
	if written.Persona == "pirate" || written.Finalechat.Enabled != nil && !*written.Finalechat.Enabled {
		t.Fatalf("config --write wrote persona %q finalechat.enabled %v from the environment\n%s", written.Persona, written.Finalechat.Enabled, raw)
	}
}
