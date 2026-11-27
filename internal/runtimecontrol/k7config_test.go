package runtimecontrol

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
)

// A preset saved from a running session (/save_preset through the
// controller) holds the project file's other settings, as main's
// ConfigFromValues did (config.LoadBundle(project, "", "")). The values
// handed to ConfigFromRoutes already name the preset being saved
// (applyValues sets Config to "bundle:"+name), so Resolve looks for a bundle
// that does not exist yet and the saved preset is the built-in defaults,
// described as coming from "bundle mine and the project file".
func TestSR2ConfigSessionPresetKeepsTheProjectFile(t *testing.T) {
	relay.OfflineCatalogs()
	cleanPresetEnv(t)
	t.Setenv("EAGENT_PERSONA", "")
	t.Setenv("EAGENT_ROLLOVER_TOKENS", "")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.BundlePath(project, "x")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"persona":"concise","rollover_tokens":123456}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadBundle(project, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Persona != "concise" || cfg.RolloverTokens != 123456 {
		t.Fatalf("session loaded persona %q rollover %d", cfg.Persona, cfg.RolloverTokens)
	}
	for _, n := range []string{cfg.Model.APIKeyEnv} {
		if n != "" {
			t.Setenv(n, "x")
		}
	}
	c, err := New(project, "s-sr2", FromConfigFor(project, cfg), func(Request, Values, map[string]any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	c.Routes = func(map[string]Route) (map[string]config.ModelRoute, error) {
		return map[string]config.ModelRoute{RouteEagent: cfg.Model}, nil
	}
	defer c.Close()
	defer runLoop(t, c)()
	view, err := View(project, "s-sr2", c.Generation, c.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := Grant(project, "s-sr2")
	prop := control.Proposal{Operation: "settings.apply", SchemaVersion: Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation,
		Edits: []control.Edit{{Op: "set", Key: "/save_preset", Value: "mine"}}}
	raw, _ := json.Marshal(prop)
	req := Request{ID: newID(), UserID: "u1", Proposal: prop, Digest: artifact.Digest(raw), Expires: time.Now().Add(5 * time.Second), Grant: grant}
	if outcome, err := Deliver(context.Background(), project, "s-sr2", req, false); err != nil || outcome.Status != "succeeded" {
		t.Fatalf("save: status=%s result=%v err=%v", outcome.Status, outcome.Result, err)
	}
	data, err := os.ReadFile(config.BundlePath(project, "mine"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	desc, _ := saved["description"].(string)
	t.Logf("saved persona=%v rollover_tokens=%v description=%q", saved["persona"], saved["rollover_tokens"], desc)
	if saved["persona"] != "concise" || saved["rollover_tokens"] != float64(123456) {
		t.Errorf("saved preset has persona %v and rollover_tokens %v, want the project file's concise and 123456", saved["persona"], saved["rollover_tokens"])
	}
	if strings.Contains(desc, "bundle mine") {
		t.Errorf("saved preset says its settings came from itself: %q", desc)
	}
}
