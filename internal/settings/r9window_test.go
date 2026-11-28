package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// rv9Old is BundleConfig's reading of a config as 12aec43 had it: the
// JSON decoded straight onto the defaults, unknown fields refused.
func rv9Old(t *testing.T, raw []byte) config.Config {
	t.Helper()
	cfg := config.Defaults()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("12aec43 reading: %v", err)
	}
	cfg.Name, cfg.DefaultConfig, cfg.Instructions = "", "", ""
	return cfg
}

func rv9WriteFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// rv9Loaded writes raw as bundle name and loads it as a session would.
func rv9Loaded(t *testing.T, project, name string, raw []byte) config.Config {
	t.Helper()
	rv9WriteFile(t, config.BundlePath(project, name), raw)
	cfg, err := config.LoadBundle(project, "", name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return cfg
}

// rv9Strip is what the web UI's saveBundleDialog sends: the effective
// configuration without name, description, instructions, preset and
// default_config (config.js strip).
func rv9Strip(t *testing.T, cfg config.Config) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(cfg)
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "description", "instructions", "preset", "default_config"} {
		delete(m, k)
	}
	return m
}

// A project on openai-high whose fallback's context window the user set to
// 0 saves a preset of it (0 kept), saves over it, then undoes that second
// save. The undo puts back the bytes of the first save; at 12aec43 they
// loaded again with the fallback's window 0, now with 1,050,000.
func TestRV9UndoPutsBackFallbackWindow(t *testing.T) {
	project := t.TempDir()
	rv9WriteFile(t, config.File(project), []byte(`{"preset":"openai-high"}`))
	s := &Service{Project: project}
	eff, err := config.LoadBundle(project, "", "")
	if err != nil || eff.Model.Fallback == nil {
		t.Fatalf("openai-high: %v (fallback %v)", err, eff.Model.Fallback)
	}
	body := rv9Strip(t, eff)
	// The window field typed as 0 (config.js num: a[f] = 0).
	body["model"].(map[string]any)["fallback"].(map[string]any)["context_tokens"] = 0
	raw, _ := json.Marshal(body)
	path, err := s.SaveBundle(context.Background(), BundleRequest{Name: "mine", Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := config.LoadBundle(project, "", "mine")
	if err != nil {
		t.Fatal(err)
	}
	if w := saved.Model.Fallback.ContextTokens; w != 0 {
		t.Fatalf("setup: the save itself gave the fallback window %d, want 0", w)
	}

	// The undo of a later save of "mine": its former bytes go back.
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	change := ResourceChange{Kind: "bundle", Name: "mine", Before: first, BeforeExists: true}
	inverse, err := change.Inverse()
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, view.Snapshot.Version
	editor, err := config.LockEditor(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := s.PrepareRestore(editor, inverse, s.Grant(), first)
	editor.Close()
	if err != nil {
		t.Fatalf("undo refused: %v", err)
	}

	old, err := config.BundleBytes("mine", "", rv9Old(t, first))
	if err != nil {
		t.Fatal(err)
	}
	oldWindow := rv9Loaded(t, project, "old", old).Model.Fallback.ContextTokens
	got := rv9Loaded(t, project, "mine", restore.After).Model.Fallback.ContextTokens
	t.Logf("fallback window: before the undo 0; after it at 12aec43 %d; now %d", oldWindow, got)
	if got != 0 {
		t.Fatalf("undo put back the fallback's context window as %d, it was 0 (12aec43 put back %d)", got, oldWindow)
	}
}

// Save as preset from the web UI, of a setup whose fallback window is 0 in
// the project file: at 12aec43 the preset loads with the fallback's window
// 0, as the setup it was saved from; now it loads with 1,050,000.
func TestRV9SaveAsPresetKeepsFallbackWindow(t *testing.T) {
	project := t.TempDir()
	rv9WriteFile(t, config.File(project), []byte(`{"preset":"openai-high","model":{"fallback":{"context_tokens":0}}}`))
	s := &Service{Project: project}
	eff := config.Resolve(project, "", "").Effective
	if eff.Model.Fallback == nil || eff.Model.Fallback.ContextTokens != 0 {
		t.Fatalf("setup: effective fallback %+v", eff.Model.Fallback)
	}
	raw, _ := json.Marshal(rv9Strip(t, eff))
	if _, err := s.SaveBundle(context.Background(), BundleRequest{Name: "mine", Config: raw}); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadBundle(project, "", "mine")
	if err != nil {
		t.Fatal(err)
	}
	old, err := config.BundleBytes("old", "", rv9Old(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	oldWindow := rv9Loaded(t, project, "old", old).Model.Fallback.ContextTokens
	t.Logf("fallback window: setup 0; saved preset at 12aec43 %d; now %d", oldWindow, got.Model.Fallback.ContextTokens)
	if w := got.Model.Fallback.ContextTokens; w != 0 {
		t.Fatalf("saved preset's fallback context window = %d, the setup saved had 0 (12aec43 saved %d)", w, oldWindow)
	}
}
