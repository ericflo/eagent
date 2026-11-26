package config

import (
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

// What the settings page and the phone are shown is what a session loads:
// a saved configuration named by default_config, or by EAGENT_CONFIG, is
// in force in both.
func TestResolveShowsTheSavedConfigurationASessionLoads(t *testing.T) {
	t.Setenv("EAGENT_PRESET", "")
	for _, tc := range []struct {
		name, file, env string
	}{
		{"default_config", `{"default_config":"slow","max_calls_per_turn":7}`, ""},
		{"EAGENT_CONFIG", `{"max_calls_per_turn":7}`, "slow"},
		{"EAGENT_CONFIG over default_config", `{"default_config":"other","max_calls_per_turn":7}`, "slow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			t.Setenv("EAGENT_CONFIG", tc.env)
			writeProjectFile(t, File(project), tc.file)
			writeProjectFile(t, BundlePath(project, "slow"), `{"max_calls_per_turn":3}`)
			writeProjectFile(t, BundlePath(project, "other"), `{"max_calls_per_turn":5}`)
			loaded, err := LoadBundle(project, "", "")
			if err != nil {
				t.Fatal(err)
			}
			res := Resolve(project, "", "")
			if res.LoadError != "" {
				t.Fatal(res.LoadError)
			}
			if loaded.MaxCallsPerTurn != 3 || res.Effective.MaxCallsPerTurn != loaded.MaxCallsPerTurn {
				t.Fatalf("a session loads %d, the editor shows %d", loaded.MaxCallsPerTurn, res.Effective.MaxCallsPerTurn)
			}
			if res.Active != (Active{Kind: "bundle", Name: "slow"}) || res.Sources["/max_calls_per_turn"] != "bundle:slow" {
				t.Fatalf("active = %+v, source = %q", res.Active, res.Sources["/max_calls_per_turn"])
			}
			// A bundle asked for by name still wins over both.
			if got := Resolve(project, "", "other").Effective.MaxCallsPerTurn; got != 5 {
				t.Fatalf("named bundle gives %d", got)
			}
		})
	}
}
