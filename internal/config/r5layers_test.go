package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeProjectFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A saved preset with no default agent loads with none, not with the one
// the project file names below it.
func TestSavedPresetWithoutADefaultAgentDoesNotTakeTheFiles(t *testing.T) {
	t.Setenv("EAGENT_PRESET", "")
	t.Setenv("EAGENT_CONFIG", "")
	project := t.TempDir()
	writeProjectFile(t, File(project), `{"relay":{"default":"codex"}}`)
	cfg := Defaults()
	raw, err := BundleBytes("plain", "", cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectFile(t, BundlePath(project, "plain"), string(raw))
	loaded, err := LoadBundle(project, "", "plain")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Relay.Default != "" {
		t.Fatalf("relay.default = %q from %s", loaded.Relay.Default, raw)
	}
}

// A field cleared in a preset's fallback route stays cleared once saved
// over that preset: the file says null for it, as it does for the main
// route, instead of a fallback object the preset's own values fill in.
func TestSavingOverAPresetKeepsAFallbackFieldCleared(t *testing.T) {
	t.Setenv("EAGENT_PRESET", "")
	t.Setenv("EAGENT_CONFIG", "")
	project := t.TempDir()
	cfg := Defaults()
	Presets["openai-high"](&cfg)
	if cfg.Model.Fallback == nil || cfg.Model.Fallback.ReasoningEffort == "" || cfg.Model.Fallback.MaxTokens == 0 {
		t.Fatalf("the preset's fallback has no effort or ceiling: %+v", cfg.Model.Fallback)
	}
	cfg.Model.Fallback.ReasoningEffort = ""
	cfg.Model.Fallback.MaxTokens = 0
	edits, err := Overlay(cfg, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	file := map[string]json.RawMessage{}
	for k, v := range edits {
		if v != nil {
			file[k] = v
		}
	}
	loaded, err := LoadBundleWithFile(project, "", "", file)
	if err != nil {
		t.Fatal(err)
	}
	if f := loaded.Model.Fallback; f == nil || f.ReasoningEffort != "" || f.MaxTokens != 0 || f.Model != cfg.Model.Fallback.Model {
		raw, _ := json.Marshal(file)
		t.Fatalf("fallback loads as %+v from %s", f, raw)
	}
}
