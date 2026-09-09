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
	if cfg, err = LoadBundle(dir, "", "deepseek"); err != nil || cfg.Preset != "deepseek" || cfg.Task.Model != "deepseek-ai/DeepSeek-V4-Flash-0731" {
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
