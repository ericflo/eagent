package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// "Save as" from an existing bundle copies what that bundle runs with from
// the files, never values only the environment gives.
func TestK6ABundleSavedFromAnotherLeavesTheEnvironmentOut(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	writeProjectFile(t, project, `{"preset":"openai-high"}`)
	if err := os.MkdirAll(filepath.Dir(config.BundlePath(project, "mine")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"persona":"concise","rollover_tokens":123456}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_PERSONA", "pirate")
	t.Setenv("EAGENT_ROLLOVER_TOKENS", "99999")
	if _, err := (&Service{Project: project}).SaveBundle(context.Background(), BundleRequest{Name: "copy", From: "mine"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(config.BundlePath(project, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Persona        string `json:"persona"`
		RolloverTokens int    `json:"rollover_tokens"`
	}
	_ = json.Unmarshal(raw, &saved)
	if saved.Persona != "concise" || saved.RolloverTokens != 123456 {
		t.Fatalf("saved persona %q rollover %d, want the bundle's concise and 123456\n%s", saved.Persona, saved.RolloverTokens, raw)
	}
}
