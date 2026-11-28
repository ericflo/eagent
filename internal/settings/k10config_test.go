package settings

import (
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// sr5UndoUnderDefaultBundle: a saved preset chosen as the project's default
// (default_config, as `eagent presets use mine` writes, or EAGENT_CONFIG)
// that runs another model than the file. A remote edit of something that is
// not a route (tick_seconds) is applied, then undone from the phone. On main
// the undo only compares the keys it puts back with the file; here it
// resolves each route over today's layers, and the "now" side is
// config.Resolve, which leaves the default bundle out, while the undone side
// is LoadBundleWithFile, which lays it.
func sr5UndoUnderDefaultBundle(t *testing.T, file string) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	writeProjectFile(t, project, file)
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"preset":"openai-high"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "mine" || loaded.Model.Model == config.Defaults().Model.Model {
		t.Fatalf("setup: bundle %q model %q, want mine's own model", loaded.Name, loaded.Model.Model)
	}
	s := &Service{Project: project}
	before, _ := os.ReadFile(config.File(project))
	edits := []control.Edit{{Op: "set", Key: "/tick_seconds", Value: float64(9)}}
	view := apply(t, s, edits...)
	desired, _ := os.ReadFile(config.File(project))
	after, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	inverse, err := ConfigInverse(control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}, string(before), string(desired))
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, after.Snapshot.Version
	if _, _, _, err := s.PrepareUndo(inverse, string(before), string(desired), s.Grant()); err != nil {
		t.Fatalf("undo of a tick_seconds edit, nothing changed since, under a default saved preset: %v", err)
	}
}

func TestSR5UndoUnderDefaultConfigBundle(t *testing.T) {
	sr5UndoUnderDefaultBundle(t, `{"default_config":"mine","tick_seconds":5}`)
}

// The same with a built-in chosen as the bundle (36d3c79's case).
func TestSR5UndoUnderDefaultConfigBuiltin(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	writeProjectFile(t, project, `{"default_config":"anthropic-med","tick_seconds":5}`)
	if loaded, err := config.Load(project, ""); err != nil || loaded.Model.Model != "claude-opus-5-5" {
		t.Fatalf("setup: %q %v", loaded.Model.Model, err)
	}
	s := &Service{Project: project}
	before, _ := os.ReadFile(config.File(project))
	edits := []control.Edit{{Op: "set", Key: "/tick_seconds", Value: float64(9)}}
	view := apply(t, s, edits...)
	desired, _ := os.ReadFile(config.File(project))
	after, _ := s.RemoteSnapshot(s.Grant())
	inverse, err := ConfigInverse(control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}, string(before), string(desired))
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, after.Snapshot.Version
	if _, _, _, err := s.PrepareUndo(inverse, string(before), string(desired), s.Grant()); err != nil {
		t.Fatalf("undo of a tick_seconds edit under default_config anthropic-med: %v", err)
	}
}

// Control: the same undo with no default bundle goes through.
func TestSR5UndoControlNoBundle(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	writeProjectFile(t, project, `{"preset":"openai-high","tick_seconds":5}`)
	s := &Service{Project: project}
	before, _ := os.ReadFile(config.File(project))
	edits := []control.Edit{{Op: "set", Key: "/tick_seconds", Value: float64(9)}}
	view := apply(t, s, edits...)
	desired, _ := os.ReadFile(config.File(project))
	after, _ := s.RemoteSnapshot(s.Grant())
	inverse, err := ConfigInverse(control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}, string(before), string(desired))
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, after.Snapshot.Version
	if _, _, _, err := s.PrepareUndo(inverse, string(before), string(desired), s.Grant()); err != nil {
		t.Fatalf("control: %v", err)
	}
}

func TestSR5UndoUnderEnvConfigBundle(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_CONFIG", "mine")
	// clearRouteEnv in the helper clears EAGENT_CONFIG again; set it after.
	sr5UndoUnderDefaultBundleEnv(t)
}

func sr5UndoUnderDefaultBundleEnv(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	writeProjectFile(t, project, `{"tick_seconds":5}`)
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"preset":"openai-high"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(project, "")
	if err != nil || loaded.Name != "mine" {
		t.Fatalf("setup: %v %q", err, loaded.Name)
	}
	s := &Service{Project: project}
	before, _ := os.ReadFile(config.File(project))
	edits := []control.Edit{{Op: "set", Key: "/tick_seconds", Value: float64(9)}}
	view := apply(t, s, edits...)
	desired, _ := os.ReadFile(config.File(project))
	after, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	inverse, err := ConfigInverse(control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}, string(before), string(desired))
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, after.Snapshot.Version
	if _, _, _, err := s.PrepareUndo(inverse, string(before), string(desired), s.Grant()); err != nil {
		t.Fatalf("undo of a tick_seconds edit, nothing changed since, under EAGENT_CONFIG: %v", err)
	}
}
