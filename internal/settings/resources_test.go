package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/protocol/control"
)

func TestPromptResourceUsesSharedValidationAndConditionalIntent(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: "prompt.set", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Parameters: map[string]any{"name": "PERSONA.md", "text": "Patient and concise."}}
	editor, err := config.LockEditor(context.Background(), s.Project)
	if err != nil {
		t.Fatal(err)
	}
	defer editor.Close()
	change, err := s.PrepareResource(editor, p, s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Apply(editor); err != nil {
		t.Fatal(err)
	}
	set, err := prompts.Load(s.Project)
	if err != nil || set.Text("PERSONA.md") != "Patient and concise." {
		t.Fatalf("saved prompt: %v", err)
	}
	updated, err := s.RemoteSnapshot(s.Grant())
	if err != nil || updated.Snapshot.Version == view.Snapshot.Version {
		t.Fatal("prompt mutation did not change resource version")
	}
	if _, err := s.PrepareResource(editor, p, s.Grant()); err == nil {
		t.Fatal("stale prompt proposal accepted")
	}
	if _, err := editor.WriteRelated("prompt", "PERSONA.md", []byte("Another writer."), false); err != nil {
		t.Fatal(err)
	}
	if err := change.Apply(editor); err == nil {
		t.Fatal("prepared prompt overwrote a later change")
	}
}

func TestPromptTemplateAndDestinationGuards(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	for _, name := range []string{"../config.json", "OTHER.md"} {
		if _, err := s.SavePrompt(context.Background(), name, "text", false); err == nil {
			t.Fatal("accepted arbitrary prompt", name)
		}
	}
	if _, err := s.SavePrompt(context.Background(), "PERSONA.md", "{{if}}", false); err == nil {
		t.Fatal("accepted invalid template")
	}
	if err := os.MkdirAll(prompts.Dir(s.Project), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(prompts.Dir(s.Project), "PERSONA.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePrompt(context.Background(), "PERSONA.md", "replacement", false); err == nil {
		t.Fatal("followed prompt symlink")
	}
	if raw, _ := os.ReadFile(outside); string(raw) != "original" {
		t.Fatal("changed outside resource")
	}
}

func TestBundleResourceAndActiveDeletionGuard(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	if _, err := s.SaveBundle(context.Background(), BundleRequest{Name: "fixture", From: "glm"}); err != nil {
		t.Fatal(err)
	}
	if _, err := config.SaveFile(s.Project, map[string]json.RawMessage{"default_config": json.RawMessage(`"fixture"`)}, "", false); err != nil {
		t.Fatal(err)
	}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	editor, err := config.LockEditor(context.Background(), s.Project)
	if err != nil {
		t.Fatal(err)
	}
	defer editor.Close()
	p := control.Proposal{Operation: "bundle.delete", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Parameters: map[string]any{"name": "fixture"}}
	if _, err := s.PrepareResource(editor, p, s.Grant()); err == nil {
		t.Fatal("deleted active default bundle")
	}
	p.Operation = "bundle.save"
	p.Parameters = map[string]any{"name": "unsafe", "config_json": `{"model":{"base_url":"https://unregistered.invalid","api_key_env":"SECRET"}}`}
	if _, err := s.PrepareResource(editor, p, s.Grant()); err == nil {
		t.Fatal("accepted unregistered endpoint/key through bundle action")
	}
}

// default_config may only name a bundle the loader accepts: a listed file
// with a name the loader rejects would make every later load fail.
func TestDefaultConfigMustBeLoadable(t *testing.T) {
	s := &Service{Project: t.TempDir()}
	if err := os.MkdirAll(config.BundlesDir(s.Project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.BundlesDir(s.Project), "prod v2.json"), []byte(`{"preset":"glm"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(config.Defaults())
	bad := "prod v2"
	if _, err := s.Save(context.Background(), Request{Config: cfg, Default: &bad}); err == nil || !strings.Contains(err.Error(), "bundle names") {
		t.Fatalf("an unloadable default_config was accepted: %v", err)
	}
	if _, err := s.SaveBundle(context.Background(), BundleRequest{Name: "fine", From: "glm"}); err != nil {
		t.Fatal(err)
	}
	good := "fine"
	if _, err := s.Save(context.Background(), Request{Config: cfg, Default: &good}); err != nil {
		t.Fatalf("a loadable default_config was refused: %v", err)
	}
}

func TestSavePresetViaRemoteApplyAndBundleSuggestions(t *testing.T) {
	project := t.TempDir()
	s := &Service{Project: project}
	g := s.Grant()

	v0, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	foundField := false
	for _, f := range v0.Descriptor.Fields {
		if f.Key == "/save_preset" {
			foundField = true
			if f.Section != "Models" || f.Group != "Configuration" || f.Class != "permissions" || !f.Writable {
				t.Fatalf("/save_preset field = %+v", f)
			}
		}
	}
	if !foundField {
		t.Fatal("descriptor missing /save_preset field")
	}

	// Built-in preset name collision is rejected.
	clash := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   SchemaVersion,
		ExpectedVersion: v0.Snapshot.Version,
		Edits:           []control.Edit{{Op: "set", Key: "/save_preset", Value: "openai-high"}},
	}
	if _, _, err := s.Apply(context.Background(), clash, g); err == nil {
		t.Fatal("accepted built-in preset name via /save_preset")
	}

	// Narrow grant without permissions cannot save a preset.
	narrow := g
	narrow.Classes = []string{"preference"}
	standalone := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   SchemaVersion,
		ExpectedVersion: v0.Snapshot.Version,
		Edits:           []control.Edit{{Op: "set", Key: "/save_preset", Value: "my-baseline"}},
	}
	if _, _, err := s.Apply(context.Background(), standalone, narrow); err == nil {
		t.Fatal("narrow grant bypassed permissions check on /save_preset")
	}

	// Standalone /save_preset writes the bundle without creating config.json.
	_, v1, err := s.Apply(context.Background(), standalone, g)
	if err != nil {
		t.Fatalf("standalone /save_preset: %v", err)
	}
	if v1.Snapshot.Version == v0.Snapshot.Version {
		t.Fatal("saving preset did not advance snapshot version")
	}
	if _, err := os.Stat(config.File(project)); !os.IsNotExist(err) {
		t.Fatal("standalone /save_preset unexpectedly created config.json")
	}
	// /default_config unlocks and offers the bundle. Its suggestion list,
	// which adds what the bundle runs, comes only when the descriptor has
	// room for a ninth list beside every relay agent's models (it holds
	// eight at most).
	bundles, listed := v1.Descriptor.Suggestions["bundles"]
	if listed && (len(bundles) != 1 || bundles[0].Value != "my-baseline") || !listed && len(v1.Descriptor.Suggestions) < 8 {
		t.Fatalf("bundles suggestions = %+v (%d lists)", bundles, len(v1.Descriptor.Suggestions))
	}
	for _, f := range v1.Descriptor.Fields {
		if f.Key == "/default_config" {
			if !f.Writable || !slices.Contains(f.Shape.Enum, any("my-baseline")) || listed != (f.Suggest != nil && f.Suggest.List == "bundles") {
				t.Fatalf("/default_config after save = %+v", f)
			}
		}
	}

	// Combined config edit + /save_preset updates config.json (without save_preset)
	// and saves the updated configuration as the new preset.
	combined := control.Proposal{
		Operation:       "settings.apply",
		SchemaVersion:   SchemaVersion,
		ExpectedVersion: v1.Snapshot.Version,
		Edits: []control.Edit{
			{Op: "set", Key: "/max_calls_per_turn", Value: float64(7)},
			{Op: "set", Key: "/save_preset", Value: "fast-seven"},
		},
	}
	_, v2, err := s.Apply(context.Background(), combined, g)
	if err != nil {
		t.Fatalf("combined edit + /save_preset: %v", err)
	}
	rawCfg, err := os.ReadFile(config.File(project))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawCfg), "save_preset") {
		t.Fatalf("config.json leaked save_preset key: %s", rawCfg)
	}
	loaded, err := config.Load(project, "fast-seven")
	if err != nil || loaded.MaxCallsPerTurn != 7 {
		t.Fatalf("saved preset fast-seven = %+v err=%v", loaded, err)
	}
	if _, exists := v2.Snapshot.Saved["/save_preset"]; exists {
		t.Fatal("/save_preset should remain unset after saving")
	}

	// DeleteBundle cleans up the saved preset.
	if err := s.DeleteBundle(context.Background(), "fast-seven"); err != nil {
		t.Fatalf("DeleteBundle: %v", err)
	}
	if err := s.DeleteBundle(context.Background(), "fast-seven"); err == nil {
		t.Fatal("DeleteBundle succeeded on missing preset")
	}
}

// An override of a retired prompt (NARRATOR.md and the rest) is read by
// nothing. The editors can see it and delete it, and never create or save
// one.
func TestRetiredPromptOverridesCanOnlyBeDeleted(t *testing.T) {
	if !slices.Equal(config.RetiredPromptNames, prompts.Retired) {
		t.Fatalf("config.RetiredPromptNames %v != prompts.Retired %v", config.RetiredPromptNames, prompts.Retired)
	}
	ctx := context.Background()
	s := &Service{Project: t.TempDir()}
	if err := os.MkdirAll(prompts.Dir(s.Project), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string) string {
		t.Helper()
		path := filepath.Join(prompts.Dir(s.Project), name)
		if err := os.WriteFile(path, []byte("Never say 'great news'."), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	exists := func(path string) bool { _, err := os.Stat(path); return err == nil }

	// Never written: not by the web editor's path, not through the editor.
	for _, name := range prompts.Retired {
		if _, err := s.SavePrompt(ctx, name, "new text", false); err == nil || !strings.Contains(err.Error(), "can only be deleted") {
			t.Fatalf("SavePrompt %s: %v", name, err)
		}
		if exists(filepath.Join(prompts.Dir(s.Project), name)) {
			t.Fatalf("%s was created", name)
		}
	}
	editor, err := config.LockEditor(ctx, s.Project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editor.WriteRelated("prompt", "NARRATOR.md", []byte("x"), false); err == nil {
		t.Fatal("WriteRelated wrote a retired prompt")
	}
	editor.Close()

	// The web editor's delete (PromptName accepts the short name too).
	path := write("NARRATOR.md")
	name, err := PromptName("narrator")
	if err != nil || name != "NARRATOR.md" {
		t.Fatalf("PromptName: %q %v", name, err)
	}
	if _, err := s.SavePrompt(ctx, name, "", true); err != nil || exists(path) {
		t.Fatalf("delete: %v (still there: %v)", err, exists(path))
	}

	// The phone: listed, not editable; prompt.reset deletes it; prompt.set
	// is refused; and the deletion cannot be undone into a write.
	path = write("ORCHESTRATOR.md")
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := view.Snapshot.Details["prompts"].(map[string]any)["ORCHESTRATOR.md"].(map[string]any)
	if entry == nil || entry["retired"] != true || entry["editable"] != false {
		t.Fatalf("snapshot entry = %v", entry)
	}
	editor, err = config.LockEditor(ctx, s.Project)
	if err != nil {
		t.Fatal(err)
	}
	defer editor.Close()
	set := control.Proposal{Operation: "prompt.set", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Parameters: map[string]any{"name": "ORCHESTRATOR.md", "text": "changed"}}
	if _, err := s.PrepareResource(editor, set, s.Grant()); err == nil {
		t.Fatal("prompt.set accepted a retired prompt")
	}
	reset := control.Proposal{Operation: "prompt.reset", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Parameters: map[string]any{"name": "ORCHESTRATOR.md"}}
	change, err := s.PrepareResource(editor, reset, s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Apply(editor); err != nil || exists(path) {
		t.Fatalf("reset: %v (still there: %v)", err, exists(path))
	}
	if _, err := change.Inverse(); err == nil {
		t.Fatal("undo of a retired override's deletion would write it back")
	}
	after, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	if _, listed := after.Snapshot.Details["prompts"].(map[string]any)["ORCHESTRATOR.md"]; listed || after.Snapshot.Version == view.Snapshot.Version {
		t.Fatalf("after reset: listed %v, version changed %v", listed, after.Snapshot.Version != view.Snapshot.Version)
	}
}
