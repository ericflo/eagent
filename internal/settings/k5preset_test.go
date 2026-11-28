package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// A preset saved from the phone holds what the configuration runs with from
// its files and bundle, never values only the environment gives: alone
// (PrepareSavePreset) or with another edit (SaveEffectiveAsPreset).
func TestK5ASavedPresetLeavesTheEnvironmentOut(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edits []control.Edit
	}{
		{"alone", nil},
		{"with an edit", []control.Edit{{Op: "set", Key: "/tick_seconds", Value: float64(30)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			s := &Service{Project: project, Bundle: "mine"}
			edits := append(tc.edits, control.Edit{Op: "set", Key: "/save_preset", Value: "saved"})
			if err := tryApply(t, s, edits...); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(config.BundlePath(project, "saved"))
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
		})
	}
}
