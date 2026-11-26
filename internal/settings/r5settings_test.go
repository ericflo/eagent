package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
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

// The editor shows what the saved default configuration sets, but a save
// of something else writes none of it into config.json: the file keeps its
// own values for those fields (or none), as it does for fields the
// environment pins.
func TestSaveLeavesWhatTheDefaultBundleSetsOutOfTheFile(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeFile(t, config.File(project), `{"default_config":"slow","max_calls_per_turn":7}`)
	writeFile(t, config.BundlePath(project, "slow"), `{"max_calls_per_turn":3,"tick_seconds":9}`)
	res := config.Resolve(project, "", "")
	if res.LoadError != "" {
		t.Fatal(res.LoadError)
	}
	cfg := res.Effective
	cfg.Persona = "terse"
	raw, _ := json.Marshal(cfg)
	out, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw, BasePreset: res.File.Preset})
	if err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(config.File(project))
	var file map[string]json.RawMessage
	if err := json.Unmarshal(written, &file); err != nil {
		t.Fatal(err)
	}
	if string(file["max_calls_per_turn"]) != "7" || file["tick_seconds"] != nil || string(file["persona"]) != `"terse"` || string(file["default_config"]) != `"slow"` {
		t.Fatalf("config.json = %s", written)
	}
	if out.Resolution.Effective.MaxCallsPerTurn != 3 || out.Resolution.Effective.Persona != "terse" {
		t.Fatalf("after the save: %+v", out.Resolution.Effective)
	}
}
