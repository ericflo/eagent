package relay

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAgentDefaultsReadTheAgentsOwnConfiguration(t *testing.T) {
	codex, claude := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("ANTHROPIC_MODEL", "")
	if m, e := AgentDefaults("codex"); m != "" || e != "" {
		t.Fatalf("no config read as %q %q", m, e)
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(codex, "config.toml"), "# mine\nmodel = \"gpt-6.2-astra\"\nmodel_reasoning_effort = 'xhigh'\n\n[projects.\"/x\"]\nmodel = \"other\"\n")
	if m, e := AgentDefaults("codex"); m != "gpt-6.2-astra" || e != "xhigh" {
		t.Fatalf("codex defaults %q %q", m, e)
	}
	write(filepath.Join(claude, "settings.json"), `{"model":"fable","effortLevel":"medium"}`)
	if m, e := AgentDefaults("claude"); m != "fable" || e != "medium" {
		t.Fatalf("claude defaults %q %q", m, e)
	}
	t.Setenv("ANTHROPIC_MODEL", "opus")
	if m, _ := AgentDefaults("claude"); m != "opus" {
		t.Fatalf("ANTHROPIC_MODEL not honoured: %q", m)
	}
	if got := Label("claude", t.TempDir(), "", ""); got != "Claude Code · Opus medium" {
		t.Fatalf("label %q", got)
	}
	if got := Label("codex", t.TempDir(), "gpt-6.2-sol", "low"); got != "Codex · gpt-6.2-sol low" {
		t.Fatalf("label %q", got)
	}
}

func TestCodexChoicesComeFromItsModelCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	// Without a cache: eagent's catalog, and the levels Codex takes.
	if len(AgentModels("codex", t.TempDir())) == 0 || !slices.Contains(AgentEfforts("codex"), "xhigh") {
		t.Fatal("no fallback choices for codex")
	}
	cache := `{"models":[
	 {"slug":"gpt-6.2-sol","display_name":"GPT-6.2-Sol","description":"Fast.","visibility":"list","supported_reasoning_levels":[{"effort":"low"},{"effort":"high"},{"effort":"ultra"}]},
	 {"slug":"hidden","display_name":"Hidden","visibility":"hide","supported_reasoning_levels":[{"effort":"secret"}]},
	 {"slug":"gpt-6.2-astra","display_name":"GPT-6.2-Astra","visibility":"list","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"max"}]}]}`
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	models := AgentModels("codex", t.TempDir())
	if len(models) != 2 || models[0].ID != "gpt-6.2-sol" || models[0].Label != "GPT-6.2-Sol" || models[0].Detail != "Fast." || models[1].ID != "gpt-6.2-astra" {
		t.Fatalf("codex models %+v", models)
	}
	if got := AgentEfforts("codex"); !slices.Equal(got, []string{"low", "medium", "high", "max", "ultra"}) {
		t.Fatalf("codex efforts %v", got)
	}
	if got := Label("codex", t.TempDir(), "gpt-6.2-astra", "max"); got != "Codex · GPT-6.2-Astra max" {
		t.Fatalf("label %q", got)
	}
}

func TestClaudeChoicesStartWithItsAliases(t *testing.T) {
	models := AgentModels("claude", t.TempDir())
	if len(models) < 5 || models[0].ID != "fable" || models[1].ID != "opus" {
		t.Fatalf("claude models %+v", models[:min(len(models), 5)])
	}
	if !slices.ContainsFunc(models, func(m ModelChoice) bool { return m.ID == "claude-opus-5-5" }) {
		t.Fatal("catalog models missing")
	}
	// Newest first within a family, families in the aliases' order.
	if f, v := claudeVersion("claude-opus-4-5-20251101"); f != 1 || !slices.Equal(v, []int{4, 5, 20251101}) {
		t.Fatalf("version %d %v", f, v)
	}
	at := func(id string) int { return slices.IndexFunc(models, func(m ModelChoice) bool { return m.ID == id }) }
	if !(at("claude-fable-5-1") < at("claude-opus-5-5") && at("claude-opus-5-5") < at("claude-sonnet-5-5") && at("claude-sonnet-5-5") < at("claude-haiku-5-5")) {
		t.Fatalf("order %+v", models)
	}
	if got := AgentEfforts("claude"); !slices.Equal(got, []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Fatalf("claude efforts %v", got)
	}
}
