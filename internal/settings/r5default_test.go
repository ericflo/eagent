package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// With a saved preset as the project's default, a save of one field from
// the web editor writes that field to config.json and nothing from the
// preset: least of all the model of a built-in preset the saved one names.
func TestSaveUnderADefaultPresetWritesOnlyTheEditedField(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	before := `{"preset":"glm","default_config":"other","max_calls_per_turn":7}`
	writeFile(t, config.File(project), before)
	writeFile(t, config.BundlePath(project, "other"), `{"preset":"openai-high","tick_seconds":9}`)
	res := config.Resolve(project, "", "")
	if res.LoadError != "" {
		t.Fatal(res.LoadError)
	}
	cfg := res.Effective
	cfg.Persona = "terse"
	body, _ := json.Marshal(cfg)
	// What the page sends: base_preset is the file's own preset.
	if _, err := (&Service{Project: project}).Save(context.Background(), Request{Config: body, BasePreset: res.File.Preset}); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(config.File(project))
	var file map[string]json.RawMessage
	if err := json.Unmarshal(written, &file); err != nil {
		t.Fatal(err)
	}
	if string(file["persona"]) != `"terse"` || string(file["max_calls_per_turn"]) != "7" || string(file["default_config"]) != `"other"` || string(file["preset"]) != `"glm"` {
		t.Fatalf("config.json = %s", written)
	}
	for _, k := range []string{"model", "summarizer", "tick_seconds"} {
		if _, ok := file[k]; ok {
			t.Fatalf("a save of persona alone wrote %q:\nbefore: %s\nafter:  %s", k, before, written)
		}
	}
}

// A default_config naming a saved preset that is gone stops sessions from
// starting; the phone can still open the settings and clear it. (The
// project has another saved preset: with none at all the field is locked,
// as it was before this round.)
func TestPhoneCanClearADefaultConfigThatNamesAMissingPreset(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeFile(t, config.File(project), `{"default_config":"gone","max_calls_per_turn":7}`)
	writeFile(t, config.BundlePath(project, "kept"), `{"max_calls_per_turn":3}`)
	if _, err := config.LoadBundle(project, "", ""); err == nil {
		t.Fatal("a session loads with a missing default_config")
	}
	s := &Service{Project: project}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatalf("the phone cannot open the settings: %v", err)
	}
	edit := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "unset", Key: "/default_config"}}}
	if _, _, err := s.Apply(context.Background(), edit, g); err != nil {
		t.Fatalf("the phone cannot clear default_config: %v", err)
	}
	if _, err := config.LoadBundle(project, "", ""); err != nil {
		t.Fatalf("after clearing it: %v", err)
	}
}
