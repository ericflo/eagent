package runtimecontrol

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A preset saved from a running session holds what the session runs with
// from its files and bundle: its own bundle's settings, never the values
// the session process's environment gives.
func TestK4ASavedPresetHoldsTheSessionsBundleNotItsEnvironment(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.BundlePath(project, "mine")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"preset":"openai-high","tick_seconds":45}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"persona":"concise","rollover_tokens":123456}`), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := config.LoadBundle(project, "", "mine")
	if err != nil {
		t.Fatal(err)
	}
	values := FromConfigFor(project, session)
	t.Setenv("EAGENT_PERSONA", "pirate")
	t.Setenv("EAGENT_ROLLOVER_TOKENS", "99999")
	t.Setenv("EAGENT_DEFAULT_RELAY", "claude")
	saved, err := ConfigFromRoutes(project, values, map[string]config.ModelRoute{RouteEagent: session.Model})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Persona != "concise" || saved.RolloverTokens != 123456 || saved.Relay.Default == "claude" {
		t.Fatalf("saved persona %q rollover %d default relay %q, want the bundle's concise and 123456 and no relay from the environment", saved.Persona, saved.RolloverTokens, saved.Relay.Default)
	}
}
