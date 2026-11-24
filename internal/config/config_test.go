package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Orchestrator.Model != "zai-org/GLM-5.3" || cfg.Task.ReasoningEffort != "medium" {
		t.Fatalf("defaults = %+v", cfg.Orchestrator)
	}
	os.MkdirAll(filepath.Join(dir, ".agents", "eagent"), 0o755)
	os.WriteFile(File(dir), []byte(`{"orchestrator":{"model":"custom/model"},"task_concurrency":5,"persona":"terse"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Always run tests."), 0o644)
	cfg, err = Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Orchestrator.Model != "custom/model" || cfg.Orchestrator.BaseURL == "" || cfg.TaskConcurrency != 5 || cfg.Persona != "terse" {
		t.Fatalf("file overrides not applied: %+v", cfg)
	}
	if cfg.Instructions != "Always run tests." {
		t.Fatalf("instructions = %q", cfg.Instructions)
	}
	t.Setenv("EAGENT_NARRATOR_MODEL", "env/model")
	cfg, _ = Load(dir, "")
	if cfg.Narrator.Model != "env/model" {
		t.Fatal("env override not applied")
	}
}

func TestPresetsAndRoutes(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir, "astra")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Orchestrator.Model != "gpt-6-astra" || cfg.Orchestrator.Fallback == nil {
		t.Fatalf("astra preset = %+v", cfg.Orchestrator)
	}
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "k")
	routes, err := cfg.Orchestrator.Routes()
	if err != nil || len(routes) != 1 || routes[0].Model != "openai/gpt-6-astra" {
		t.Fatalf("routes = %+v %v", routes, err)
	}
	t.Setenv("OPENROUTER_API_KEY", "")
	if _, err := cfg.Orchestrator.Routes(); err == nil {
		t.Fatal("no keys should be an error")
	}
	if _, err := Load(dir, "nope"); err == nil {
		t.Fatal("unknown preset accepted")
	}
}

func TestBundlesLayerOverProjectConfig(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(BundlesDir(dir), 0o755)
	os.WriteFile(File(dir), []byte(`{"task_concurrency":2,"narrator":{"model":"project/narrator"}}`), 0o644)
	os.WriteFile(BundlePath(dir, "mine"), []byte(`{"preset":"anthropic","description":"mine","orchestrator":{"model":"claude-x"}}`), 0o644)
	cfg, err := LoadBundle(dir, "", "mine")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "mine" || cfg.Preset != "anthropic" {
		t.Fatalf("name=%q preset=%q", cfg.Name, cfg.Preset)
	}
	if cfg.Orchestrator.Model != "claude-x" || cfg.Orchestrator.Protocol != "anthropic" {
		t.Fatalf("bundle should override the model but keep the preset's protocol: %+v", cfg.Orchestrator)
	}
	if cfg.Narrator.Model != "project/narrator" {
		t.Fatalf("project config should still apply beneath the bundle: %s", cfg.Narrator.Model)
	}
	if cfg.TaskConcurrency != 2 {
		t.Fatal("project scalar lost")
	}
	// EAGENT_CONFIG selects a bundle; a preset name is accepted as a bundle name too.
	t.Setenv("EAGENT_CONFIG", "mine")
	if cfg, _ = LoadBundle(dir, "", ""); cfg.Name != "mine" {
		t.Fatal("EAGENT_CONFIG not honoured")
	}
	t.Setenv("EAGENT_CONFIG", "")
	if cfg, err = LoadBundle(dir, "", "deepseek"); err != nil || cfg.Preset != "deepseek" || cfg.Task.Model != "deepseek-ai/DeepSeek-V4.1-Flash-1017" {
		t.Fatalf("preset as bundle: %v %+v", err, cfg.Task)
	}
	if _, err := LoadBundle(dir, "", "nope"); err == nil {
		t.Fatal("unknown bundle accepted")
	}
	// default_config in config.json.
	os.WriteFile(File(dir), []byte(`{"default_config":"mine"}`), 0o644)
	if cfg, _ = LoadBundle(dir, "", ""); cfg.Name != "mine" {
		t.Fatal("default_config not honoured")
	}
	// Save round-trips.
	path, err := SaveBundle(dir, "saved", "a copy", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "saved.json" {
		t.Fatalf("path = %s", path)
	}
	names, _ := ListBundles(dir)
	if len(names) != 2 || names[0] != "mine" || names[1] != "saved" {
		t.Fatalf("names = %v", names)
	}
	again, err := LoadBundle(dir, "", "saved")
	if err != nil || again.Orchestrator.Model != "claude-x" || again.Description != "a copy" {
		t.Fatalf("round trip: %v %+v", err, again.Orchestrator)
	}
	if _, err := SaveBundle(dir, "../evil", "", cfg); err == nil {
		t.Fatal("bad name accepted")
	}
}

// The orchestrator in every preset runs at its model's highest effort below
// max, as the catalog lists the model's efforts.
func TestPresetOrchestratorsThinkHardest(t *testing.T) {
	for name, apply := range Presets {
		c := Defaults()
		apply(&c)
		m, ok := Lookup(c.Orchestrator.BaseURL, c.Orchestrator.Model)
		if !ok || len(m.Efforts) == 0 {
			continue
		}
		want := m.Efforts[0]
		for _, e := range m.Efforts {
			if e != "max" && EffortIndex(e) > EffortIndex(want) {
				want = e
			}
		}
		if c.Orchestrator.ReasoningEffort != want {
			t.Errorf("%s: orchestrator %s runs %q, want %q", name, c.Orchestrator.Model, c.Orchestrator.ReasoningEffort, want)
		}
	}
}

func TestSavedPresetResolvesViaPresetFlagRejectsBuiltInAndClearsFallback(t *testing.T) {
	dir := t.TempDir()
	// Project file uses openai-high (which carries OpenRouter fallbacks on all 3 actors).
	if err := os.MkdirAll(filepath.Dir(File(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(File(dir), []byte(`{"preset":"openai-high"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Built-in preset names and aliases cannot be overwritten by a saved preset.
	localCfg := Defaults()
	localCfg.Orchestrator = Actor{Protocol: "openai-chat", BaseURL: "http://127.0.0.1:1234/v1", Model: "local/model", ReasoningEffort: "high", MaxTokens: 8192, ContextTokens: 131072}
	localCfg.Task = localCfg.Orchestrator
	localCfg.Narrator = localCfg.Orchestrator
	for _, reserved := range []string{"glm", "openai-high", "anthropic"} {
		if _, err := SaveBundle(dir, reserved, "clash", localCfg); err == nil {
			t.Fatalf("expected built-in preset name %q to be rejected", reserved)
		}
	}

	if _, err := SaveBundle(dir, "my-local", "all local", localCfg); err != nil {
		t.Fatal(err)
	}
	// Loading via --preset (Load) OR --config (LoadBundle) resolves the saved preset
	// and explicitly clears openai-high's inherited OpenRouter fallbacks.
	viaPreset, err := Load(dir, "my-local")
	if err != nil {
		t.Fatalf("Load(my-local): %v", err)
	}
	if viaPreset.Name != "my-local" || viaPreset.Orchestrator.Model != "local/model" || viaPreset.Orchestrator.Fallback != nil || viaPreset.Task.Fallback != nil || viaPreset.Narrator.Fallback != nil {
		t.Fatalf("viaPreset leaked fallback or wrong model: %+v", viaPreset)
	}
	// EAGENT_PRESET resolves a saved preset too, overriding default_config.
	if _, err := SaveBundle(dir, "other", "other", Defaults()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(File(dir), []byte(`{"preset":"openai-high","default_config":"other"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_PRESET", "my-local")
	viaEnv, err := Load(dir, "")
	if err != nil || viaEnv.Name != "my-local" || viaEnv.Orchestrator.Model != "local/model" {
		t.Fatalf("EAGENT_PRESET saved preset: %v %+v", err, viaEnv)
	}
	t.Setenv("EAGENT_PRESET", "")

	res := Resolve(dir, "my-local", "")
	if res.LoadError != "" || res.Active.Kind != "bundle" || res.Active.Name != "my-local" || res.Effective.Orchestrator.Fallback != nil {
		t.Fatalf("Resolve(my-local): %+v", res)
	}

	// DeleteBundle guards the active default_config and deletes non-default presets.
	if err := DeleteBundle(dir, "other"); err == nil {
		t.Fatal("deleted active default_config")
	}
	if err := DeleteBundle(dir, "my-local"); err != nil {
		t.Fatalf("DeleteBundle(my-local): %v", err)
	}
	if _, err := os.Stat(BundlePath(dir, "my-local")); !os.IsNotExist(err) {
		t.Fatal("deleted preset file still exists")
	}
}
