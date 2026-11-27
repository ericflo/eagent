package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// Finding 17: a bundle that names a preset and leaves the rest to it is
// checked as it loads, over that preset: an undo of its deletion puts it
// back (it was put back on main), and a save of it keeps the preset's model.
func TestR14BundleBuildsOnItsPreset(t *testing.T) {
	for name, raw := range map[string]string{
		"today's keys": `{"preset":"openai-high","model":{"model":"gpt-6.1-sol","api_key_env":"OPENAI_API_KEY"}}`,
		"main's keys":  `{"preset":"openai-high","orchestrator":{"model":"gpt-6.1-sol","api_key_env":"OPENAI_API_KEY"}}`,
	} {
		t.Run("undo "+name, func(t *testing.T) {
			clearRouteEnv(t)
			s := &Service{Project: t.TempDir()}
			view, err := s.RemoteSnapshot(s.Grant())
			if err != nil {
				t.Fatal(err)
			}
			editor, err := config.LockEditor(context.Background(), s.Project)
			if err != nil {
				t.Fatal(err)
			}
			defer editor.Close()
			change := ResourceChange{Kind: "bundle", Name: "mine", Before: []byte(raw), BeforeExists: true}
			inverse, err := change.Inverse()
			if err != nil {
				t.Fatal(err)
			}
			inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, view.Snapshot.Version
			restore, err := s.PrepareRestore(editor, inverse, s.Grant(), []byte(raw))
			if err != nil {
				t.Fatalf("undo refused the bundle: %v", err)
			}
			if string(restore.After) != raw {
				t.Fatalf("undo writes %s, want the bytes back", restore.After)
			}
		})
	}
	t.Run("save", func(t *testing.T) {
		clearRouteEnv(t)
		s := &Service{Project: t.TempDir()}
		if _, err := s.SaveBundle(context.Background(), BundleRequest{Name: "mine", Config: json.RawMessage(`{"preset":"openai-high","tick_seconds":15}`)}); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.LoadBundle(s.Project, "", "mine")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Model.Model != "gpt-6-astra" || cfg.Model.BaseURL != "https://api.openai.com/v1" || cfg.TickSeconds != 15 {
			t.Fatalf("saved bundle loads %s @ %s, tick %d; want openai-high's gpt-6-astra, tick 15", cfg.Model.Model, cfg.Model.BaseURL, cfg.TickSeconds)
		}
	})
}
