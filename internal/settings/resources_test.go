package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	p.Parameters = map[string]any{"name": "unsafe", "config_json": `{"task":{"base_url":"https://unregistered.invalid","api_key_env":"SECRET"}}`}
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
