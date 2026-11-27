package runtimecontrol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// A session started under EAGENT_PRESET=work, a saved preset, runs bundle
// work (the loader takes it as the bundle; 8a63035's IsDefaultBundle says
// so too). A preset saved from that session resolves "bundle:work" with
// Resolve(project, "", "work"), where EAGENT_PRESET=work is then read as a
// built-in preset name, which it is not: the layers "cannot be read", and
// the preset is built from the project file instead, with a description
// saying bundle work could not be read.
func TestSR7APresetSavedUnderEagentPresetTakesTheBundleItRuns(t *testing.T) {
	cleanPresetEnv(t)
	t.Setenv("EAGENT_PERSONA", "")
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.BundlePath(project, "work")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"persona":"concise"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "work"), []byte(`{"persona":"terse"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_PRESET", "work")
	session, err := config.LoadBundle(project, "", "")
	if err != nil || session.Name != "work" || session.Persona != "terse" {
		t.Fatalf("setup: the session runs %q persona %q (%v)", session.Name, session.Persona, err)
	}
	values := FromConfigFor(project, session)
	saved, err := ConfigFromRoutes(project, values, map[string]config.ModelRoute{RouteEagent: session.Model})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Persona != "terse" || strings.Contains(saved.Description, "could not be read") {
		t.Fatalf("a preset saved from a session on bundle work has persona %q (the session runs terse) and says: %s", saved.Persona, saved.Description)
	}
}

// Under EAGENT_PRESET=work, a saved preset, a running session can choose
// another saved preset: the one it names outranks the variable, which is
// never read as a built-in preset's name.
func TestK12ASavedPresetIsChosenUnderEagentPreset(t *testing.T) {
	r3Env(t)
	project := t.TempDir()
	r10Write(t, config.BundlePath(project, "work"), `{"persona":"terse"}`)
	r10Write(t, config.BundlePath(project, "other"), `{"preset":"openai-high"}`)
	t.Setenv("EAGENT_PRESET", "work")
	session, err := config.LoadBundle(project, "", "")
	if err != nil || session.Name != "work" {
		t.Fatalf("setup: the session runs %q (%v)", session.Name, err)
	}
	out, err := r3Apply(t, project, FromConfigFor(project, session), control.Edit{Op: "set", Key: "/config", Value: "bundle:other"})
	if err != nil {
		t.Fatalf("choosing the saved preset other under EAGENT_PRESET=work: %v", err)
	}
	other, _ := config.LoadBundle(project, "openai-high", "")
	if out.Config != "bundle:other" || out.Routes[RouteEagent].Model != other.Model.Model {
		t.Fatalf("after choosing other: %s on %s, want %s", out.Config, out.Routes[RouteEagent].Model, other.Model.Model)
	}
}
