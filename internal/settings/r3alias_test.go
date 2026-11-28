package settings

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// A project file that names a preset by an older name ("openai-med" now
// stands for "openai-high") shows the preset's name today in the remote
// editor, can be edited, and undoing an edit of its preset puts back a
// name the preset field accepts.
func TestAnOlderPresetNameIsEditableAndUndoableRemotely(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	before := `{"preset":"openai-med","tick_seconds":45}`
	if err := os.WriteFile(config.File(project), []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{Project: project}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Snapshot.Saved["/preset"]; got != "openai-high" {
		t.Fatalf("saved preset = %v", got)
	}
	var presetField control.Field
	for _, f := range view.Descriptor.Fields {
		if f.Key == "/preset" {
			presetField = f
		}
	}
	if !slices.Contains(presetField.Shape.Enum, any("openai-high")) {
		t.Fatalf("preset choices = %v", presetField.Shape.Enum)
	}
	edit := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-high"}}}
	inverse, err := ConfigInverse(edit, before, `{"preset":"anthropic-high","tick_seconds":45}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(inverse.Edits) != 1 || inverse.Edits[0].Value != "openai-high" {
		t.Fatalf("inverse = %+v", inverse.Edits)
	}
	if err := presetField.Shape.Validate(inverse.Edits[0].Value); err != nil {
		t.Fatalf("the undo would be refused: %v", err)
	}
	if _, _, err := s.Apply(context.Background(), edit, g); err != nil {
		t.Fatal(err)
	}
}
