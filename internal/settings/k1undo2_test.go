package settings

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// rv15Step runs one undo the way the connector does: the reversal of a
// journal (applied, beforeRaw, desiredRaw) is computed and written through
// PrepareUndo against the file on disk now.
func rv15Step(t *testing.T, s *Service, applied control.Proposal, beforeRaw, desiredRaw string) (control.Proposal, string) {
	t.Helper()
	inverse, err := ConfigInverse(applied, beforeRaw, desiredRaw)
	if err != nil {
		t.Fatal(err)
	}
	now, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, now.Snapshot.Version
	raw, _, _, err := s.PrepareUndo(inverse, beforeRaw, desiredRaw, s.Grant())
	if err != nil {
		t.Fatalf("undo refused: %v (reversal %+v)", err, inverse.Edits)
	}
	return inverse, raw
}

func rv15Load(t *testing.T, project string) config.Config {
	t.Helper()
	cfg, err := config.Load(project, "")
	if err != nil {
		raw, _ := os.ReadFile(config.File(project))
		t.Fatalf("load: %v\n%s", err, raw)
	}
	return cfg
}

// A file with a key variable of its own: a remote /preset change, then its
// undo with nothing changed locally. At f36e19c the change left the key
// variable and the undo (/preset alone) went through.
func TestRV15UndoOfPresetChangeWithOwnKeyVar(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			project := t.TempDir()
			before := `{"preset":"openai-high","model":{"api_key_env":"OPENAI_API_KEY_WORK"}}`
			edits := []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-med"}}
			writeProjectFile(t, project, before)
			s := &Service{Project: project}
			view := apply(t, s, edits...)
			wrote, _ := os.ReadFile(config.File(project))
			cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}
			inverse, err := ConfigInverse(cmd, before, string(wrote))
			if err != nil {
				t.Fatal(err)
			}
			now, _ := s.RemoteSnapshot(s.Grant())
			inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, now.Snapshot.Version
			if _, _, _, err := s.PrepareUndo(inverse, before, string(wrote), s.Grant()); err != nil {
				t.Fatalf("undo refused: %v\nreversal offered: %+v\ncommand wrote:\n%s", err, inverse.Edits, wrote)
			}
		})
	}
}

// An undo, then the undo of that undo (a redo), both without the pick
// rules: the undo loads as the file before the command, and the redo loads
// as the file the command wrote.
func TestRV15UndoThenRedoLoadAsBeforeAndAfter(t *testing.T) {
	for _, tc := range []struct {
		name, before string
		edits        []control.Edit
	}{
		{"preset takes the model out", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol"}}`, []control.Edit{{Op: "set", Key: "/preset", Value: "glm"}}},
		{"preset clamps the effort", `{"preset":"openai-high","model":{"reasoning_effort":"xhigh","fallback":null}}`, []control.Edit{{Op: "set", Key: "/preset", Value: "glm"}}},
		{"preset with a window", `{"preset":"openai-high","model":{"context_tokens":400000,"max_tokens":4000}}`, []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-med"}}},
		{"preset unset, key var", `{"preset":"openai-high","model":{"api_key_env":"MY_OPENAI_KEY"}}`, []control.Edit{{Op: "unset", Key: "/preset"}}},
		{"model at another provider", `{"preset":"openai-high"}`, []control.Edit{{Op: "set", Key: "/model/model", Value: "zai-org/GLM-5.3"}, {Op: "set", Key: "/model/base_url", Value: "https://api.together.xyz/v1"}}},
		{"null fallback written into", `{"preset":"openai-high","model":{"fallback":null}}`, []control.Edit{{Op: "set", Key: "/model/fallback/model", Value: "openai/gpt-6.1-sol"}}},
		{"dedicated summarizer seeded", `{"preset":"openai-high","summarizer":{"mode":"dedicated"}}`, []control.Edit{{Op: "set", Key: "/summarizer/model/model", Value: "gpt-6.1-sol"}}},
		{"dedicated summarizer at another provider", `{"preset":"openai-high","summarizer":{"mode":"dedicated"}}`, []control.Edit{{Op: "set", Key: "/summarizer/model/model", Value: "zai-org/GLM-5.3-Flash"}, {Op: "set", Key: "/summarizer/model/base_url", Value: "https://api.together.xyz/v1"}}},
	} {
		for _, fc := range []string{"", "off"} {
			t.Run(tc.name+"/finalechat="+fc, func(t *testing.T) {
				clearRouteEnv(t)
				t.Setenv("EAGENT_FINALECHAT", fc)
				project := t.TempDir()
				writeProjectFile(t, project, tc.before)
				want := rv15Load(t, project)
				s := &Service{Project: project}
				view := apply(t, s, tc.edits...)
				wrote, _ := os.ReadFile(config.File(project))
				afterCmd := rv15Load(t, project)
				cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: tc.edits}
				undo, undoRaw := rv15Step(t, s, cmd, tc.before, string(wrote))
				writeProjectFile(t, project, undoRaw)
				got := rv15Load(t, project)
				if !reflect.DeepEqual(got.Model, want.Model) || got.Preset != want.Preset || !reflect.DeepEqual(got.Summarizer, want.Summarizer) {
					t.Fatalf("after undo %s %+v %+v\nwant %s %+v %+v\ncommand wrote:\n%s\nundo wrote:\n%s", got.Preset, got.Model, got.Summarizer, want.Preset, want.Model, want.Summarizer, wrote, undoRaw)
				}
				// The undo's journal: applied is the reversal, before_raw
				// the file the command wrote, desired the undo's file.
				_, redoRaw := rv15Step(t, s, undo, string(wrote), undoRaw)
				writeProjectFile(t, project, redoRaw)
				redone := rv15Load(t, project)
				if !reflect.DeepEqual(redone.Model, afterCmd.Model) || redone.Preset != afterCmd.Preset || !reflect.DeepEqual(redone.Summarizer, afterCmd.Summarizer) {
					t.Fatalf("after redo %s %+v %+v\nwant %s %+v %+v\ncommand wrote:\n%s\nundo wrote:\n%s\nredo wrote:\n%s", redone.Preset, redone.Model, redone.Summarizer, afterCmd.Preset, afterCmd.Model, afterCmd.Summarizer, wrote, undoRaw, redoRaw)
				}
			})
		}
	}
}

// The undo of a remote model pick at another provider, over a file whose
// route had a key variable of its own (refused already at f36e19c), puts
// that variable back: the file before the command paired it with its
// address. Its redo loads as the command left the file.
func TestK1UndoOfAModelPickWithOwnKeyVar(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			project := t.TempDir()
			before := `{"preset":"openai-high","model":{"api_key_env":"OPENAI_API_KEY_WORK"}}`
			edits := []control.Edit{{Op: "set", Key: "/model/model", Value: "zai-org/GLM-5.3"}, {Op: "set", Key: "/model/base_url", Value: "https://api.together.xyz/v1"}}
			writeProjectFile(t, project, before)
			want := rv15Load(t, project)
			s := &Service{Project: project}
			view := apply(t, s, edits...)
			wrote, _ := os.ReadFile(config.File(project))
			afterCmd := rv15Load(t, project)
			cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}
			undo, undoRaw := rv15Step(t, s, cmd, before, string(wrote))
			writeProjectFile(t, project, undoRaw)
			if got := rv15Load(t, project); !reflect.DeepEqual(got.Model, want.Model) {
				t.Fatalf("after undo %+v, want %+v\nundo wrote:\n%s", got.Model, want.Model, undoRaw)
			}
			_, redoRaw := rv15Step(t, s, undo, string(wrote), undoRaw)
			writeProjectFile(t, project, redoRaw)
			if got := rv15Load(t, project); !reflect.DeepEqual(got.Model, afterCmd.Model) {
				t.Fatalf("after redo %+v, want %+v\nredo wrote:\n%s", got.Model, afterCmd.Model, redoRaw)
			}
		})
	}
}

// A reversal does not pair a key variable the file before held with an
// address it was never paired with: the command changed the key, the
// address was changed locally since, and the undo putting the old key back
// is refused, naming the key.
func TestK1UndoDoesNotPairAnOldKeyWithANewAddress(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	before := `{"preset":"openai-high","model":{"api_key_env":"OPENAI_API_KEY_WORK"}}`
	edits := []control.Edit{{Op: "set", Key: "/model/api_key_env", Value: "OPENAI_API_KEY"}}
	writeProjectFile(t, project, before)
	s := &Service{Project: project}
	view := apply(t, s, edits...)
	wrote, _ := os.ReadFile(config.File(project))
	writeProjectFile(t, project, `{"preset":"openai-high","model":{"api_key_env":"OPENAI_API_KEY","base_url":"https://openrouter.ai/api/v1","model":"openai/gpt-6-astra","protocol":"openai-chat"}}`)
	cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}
	inverse, err := ConfigInverse(cmd, before, string(wrote))
	if err != nil {
		t.Fatal(err)
	}
	now, _ := s.RemoteSnapshot(s.Grant())
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, now.Snapshot.Version
	raw, _, _, err := s.PrepareUndo(inverse, before, string(wrote), s.Grant())
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY_WORK") {
		t.Fatalf("undo: %v, want it refused naming OPENAI_API_KEY_WORK (reversal %+v)\nwrote:\n%s", err, inverse.Edits, raw)
	}
}

// The undo of a remote /preset change that took out a model the file named
// without an address puts that model back. When the address it would be
// reached at was changed locally since the command, the model would go to
// an address it was never chosen for: the undo is a conflict.
func TestK1UndoPuttingBackAModelConflictsOnALocalAddressEdit(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			project := t.TempDir()
			before := `{"preset":"openai-high","model":{"model":"gpt-6.1-sol"}}`
			edits := []control.Edit{{Op: "set", Key: "/preset", Value: "glm"}}
			writeProjectFile(t, project, before)
			s := &Service{Project: project}
			view := apply(t, s, edits...)
			wrote, _ := os.ReadFile(config.File(project))
			writeProjectFile(t, project, `{"preset":"glm","model":{"base_url":"https://openrouter.ai/api/v1","api_key_env":"OPENROUTER_API_KEY","protocol":"openai-chat"}}`)
			cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}
			inverse, err := ConfigInverse(cmd, before, string(wrote))
			if err != nil {
				t.Fatal(err)
			}
			now, _ := s.RemoteSnapshot(s.Grant())
			inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, now.Snapshot.Version
			raw, _, _, err := s.PrepareUndo(inverse, before, string(wrote), s.Grant())
			var conflict *config.ErrConflict
			if !errors.As(err, &conflict) {
				t.Fatalf("undo: %v, want a conflict (reversal %+v)\nwrote:\n%s", err, inverse.Edits, raw)
			}
		})
	}
}
