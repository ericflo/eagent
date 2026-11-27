package settings

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
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

// Finding 20: a remote /preset set or unset alone is a pick of the model
// route: the effort kept moves to one the new preset's model offers, and a
// model the file names for the old preset's address, without the address,
// is not sent to the new one's. Undo puts the file back as it was.
func TestR14RemotePresetAloneIsAPick(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		edit       control.Edit
	}{
		{"effort, set glm", `{"preset":"openai-high","model":{"reasoning_effort":"xhigh"}}`, control.Edit{Op: "set", Key: "/preset", Value: "glm"}},
		{"effort, unset", `{"preset":"openai-high","model":{"reasoning_effort":"xhigh"}}`, control.Edit{Op: "unset", Key: "/preset"}},
		{"model, set glm", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol"}}`, control.Edit{Op: "set", Key: "/preset", Value: "glm"}},
		{"model, unset", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol"}}`, control.Edit{Op: "unset", Key: "/preset"}},
		{"model and effort, unset", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol","reasoning_effort":"xhigh"}}`, control.Edit{Op: "unset", Key: "/preset"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearRouteEnv(t)
			project := t.TempDir()
			writeProjectFile(t, project, tc.file)
			before, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := tryApply(t, &Service{Project: project}, tc.edit); err != nil {
				t.Fatal(err)
			}
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			written, _ := os.ReadFile(config.File(project))
			// The defaults' route (glm's), at the effort the clamp a
			// session's preset pick uses gives what the file kept.
			want := config.Defaults()
			var file struct {
				Model config.ModelRoute `json:"model"`
			}
			_ = json.Unmarshal([]byte(tc.file), &file)
			if file.Model.ReasoningEffort != "" {
				want.Model.ReasoningEffort = file.Model.ReasoningEffort
				config.OfferedEffort(project, &want)
			}
			if got.Model.Model != want.Model.Model || got.Model.BaseURL != want.Model.BaseURL || got.Model.ReasoningEffort != want.Model.ReasoningEffort {
				t.Fatalf("got %s @ %s effort %q, want %s @ %s effort %q\n%s", got.Model.Model, got.Model.BaseURL, got.Model.ReasoningEffort, want.Model.Model, want.Model.BaseURL, want.Model.ReasoningEffort, written)
			}
			raw := r11Undo(t, project, tc.file, []control.Edit{tc.edit})
			writeProjectFile(t, project, raw)
			undone, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("load after undo: %v\n%s", err, raw)
			}
			if !reflect.DeepEqual(undone.Model, before.Model) || undone.Preset != before.Preset {
				t.Fatalf("after undo %s %+v, want %s %+v\nundo wrote:\n%s", undone.Preset, undone.Model, before.Preset, before.Model, raw)
			}
		})
	}
}
