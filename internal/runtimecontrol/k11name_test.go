package runtimecontrol

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A config.json carrying a "name" (`config --write` once wrote one) laid no
// bundle: a session on it runs the file, and a preset saved from it takes the
// file's settings, not those of the bundle the name happens to match.
func TestK11ASessionOnANamedFileIsTheFile(t *testing.T) {
	cleanPresetEnv(t)
	t.Setenv("EAGENT_PERSONA", "")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.BundlePath(project, "work")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"name":"work","persona":"concise"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "work"), []byte(`{"persona":"terse"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := config.LoadBundle(project, "", "")
	if err != nil || session.Persona != "concise" {
		t.Fatalf("setup: the session runs persona %q (%v)", session.Persona, err)
	}
	values := FromConfigFor(project, session)
	saved, err := ConfigFromRoutes(project, values, map[string]config.ModelRoute{RouteEagent: session.Model})
	if err != nil {
		t.Fatal(err)
	}
	if values.Config == "bundle:work" || saved.Persona != "concise" {
		t.Fatalf("the session's configuration reads %q and a preset saved from it has persona %q; it runs the file (concise)", values.Config, saved.Persona)
	}
}
