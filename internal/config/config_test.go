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
	if cfg.Orchestrator.Model != "zai-org/GLM-5.3" || cfg.Task.ReasoningEffort != "low" {
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
