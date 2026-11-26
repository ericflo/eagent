package config

import (
	"os"
	"path/filepath"
	"testing"
)

// r10OpenAINoWindows is openai-high with the window of its route and of its
// fallback typed as 0 (unknown), as the editor leaves them.
func r10OpenAINoWindows() Config {
	cfg := Defaults()
	Presets["openai-high"](&cfg)
	cfg.Model.ContextTokens = 0
	cfg.Model.Fallback.ContextTokens = 0
	return cfg
}

func r10Windows(cfg Config) (int, int) {
	fallback := -1
	if cfg.Model.Fallback != nil {
		fallback = cfg.Model.Fallback.ContextTokens
	}
	return cfg.Model.ContextTokens, fallback
}

// A window the editor writes as null (a route's 0) loads as 0 over a layer
// with another model: a saved preset, an overlay and a pinned file, each
// over a project on the defaults (GLM-5.3). Omitting the window still
// gives a changed model its own (TestContextTokensFollowTheModel).
func TestR10NullWindowStaysClearedOverAnotherModel(t *testing.T) {
	unsetEnv(t, routeEnvNames()...)

	t.Run("saved preset", func(t *testing.T) {
		project := t.TempDir()
		raw, err := BundleBytes("mine", "", r10OpenAINoWindows())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(BundlePath(project, "mine")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(BundlePath(project, "mine"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadBundle(project, "", "mine")
		if err != nil {
			t.Fatal(err)
		}
		if p, f := r10Windows(loaded); p != 0 || f != 0 {
			t.Fatalf("LoadBundle: windows = (%d, %d), want (0, 0)", p, f)
		}
		alone, err := BundleAlone(project, "mine")
		if err != nil {
			t.Fatal(err)
		}
		if p, f := r10Windows(alone); p != 0 || f != 0 {
			t.Fatalf("BundleAlone: windows = (%d, %d), want (0, 0)", p, f)
		}
	})

	t.Run("overlay", func(t *testing.T) {
		project := t.TempDir()
		cfg := Defaults()
		cfg.Model.Model = "zai-org/GLM-5.3-Flash"
		cfg.Model.ContextTokens = 0
		edits, err := Overlay(cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := SaveFile(project, edits, "", false); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(project, "")
		if err != nil {
			t.Fatal(err)
		}
		if w := loaded.Model.ContextTokens; w != 0 {
			t.Fatalf("Overlay+Load: window = %d, want 0", w)
		}
		if w := Resolve(project, "", "").Effective.Model.ContextTokens; w != 0 {
			t.Fatalf("Overlay+Resolve: window = %d, want 0", w)
		}
	})

	t.Run("pin", func(t *testing.T) {
		project := t.TempDir()
		if _, err := SaveFile(project, Pin(r10OpenAINoWindows(), ""), "", false); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(project, "")
		if err != nil {
			t.Fatal(err)
		}
		if p, f := r10Windows(loaded); p != 0 || f != 0 {
			t.Fatalf("Pin+Load: windows = (%d, %d), want (0, 0)", p, f)
		}
	})
}
