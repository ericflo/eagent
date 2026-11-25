package config

import (
	"encoding/json"
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
	if cfg.Model.Model != "zai-org/GLM-5.3" || cfg.Model.ReasoningEffort != "high" || cfg.Summarizer.Mode != SummarizerInline || len(cfg.Migrated) != 0 {
		t.Fatalf("defaults = %+v", cfg)
	}
	os.MkdirAll(filepath.Join(dir, ".agents", "eagent"), 0o755)
	os.WriteFile(File(dir), []byte(`{"model":{"model":"custom/model"},"max_calls_per_turn":5,"persona":"terse"}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".agents", "eagent", "INSTRUCTIONS.md"), []byte("Always run tests."), 0o644)
	cfg, err = Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Model != "custom/model" || cfg.Model.BaseURL == "" || cfg.MaxCallsPerTurn != 5 || cfg.Persona != "terse" {
		t.Fatalf("file overrides not applied: %+v", cfg)
	}
	if cfg.Instructions != "Always run tests." {
		t.Fatalf("instructions = %q", cfg.Instructions)
	}
	t.Setenv("EAGENT_MODEL", "env/model")
	cfg, _ = Load(dir, "")
	if cfg.Model.Model != "env/model" {
		t.Fatal("env override not applied")
	}
}

func TestPresetsAndRoutes(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir, "astra")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Model != "gpt-6-astra" || cfg.Model.Fallback == nil {
		t.Fatalf("astra preset = %+v", cfg.Model)
	}
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "k")
	routes, err := cfg.Model.Routes()
	if err != nil || len(routes) != 1 || routes[0].Model != "openai/gpt-6-astra" {
		t.Fatalf("routes = %+v %v", routes, err)
	}
	t.Setenv("OPENROUTER_API_KEY", "")
	if _, err := cfg.Model.Routes(); err == nil {
		t.Fatal("no keys should be an error")
	}
	if _, err := Load(dir, "nope"); err == nil {
		t.Fatal("unknown preset accepted")
	}
}

func TestBundlesLayerOverProjectConfig(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(BundlesDir(dir), 0o755)
	os.WriteFile(File(dir), []byte(`{"max_calls_per_turn":2,"model":{"max_tokens":4096},"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"project/summarizer","api_key_env":"TOGETHER_API_KEY"}}}`), 0o644)
	os.WriteFile(BundlePath(dir, "mine"), []byte(`{"preset":"anthropic","description":"mine","model":{"model":"claude-x"}}`), 0o644)
	cfg, err := LoadBundle(dir, "", "mine")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "mine" || cfg.Preset != "anthropic" {
		t.Fatalf("name=%q preset=%q", cfg.Name, cfg.Preset)
	}
	if cfg.Model.Model != "claude-x" || cfg.Model.Protocol != "anthropic" {
		t.Fatalf("bundle should override the model but keep the preset's protocol: %+v", cfg.Model)
	}
	if cfg.Model.MaxTokens != 4096 || cfg.Summarizer.Route(cfg.Model).Model != "project/summarizer" {
		t.Fatalf("project config should still apply beneath the bundle: %+v %+v", cfg.Model, cfg.Summarizer)
	}
	if cfg.MaxCallsPerTurn != 2 {
		t.Fatal("project scalar lost")
	}
	// EAGENT_CONFIG selects a bundle; a preset name is accepted as a bundle name too.
	t.Setenv("EAGENT_CONFIG", "mine")
	if cfg, _ = LoadBundle(dir, "", ""); cfg.Name != "mine" {
		t.Fatal("EAGENT_CONFIG not honoured")
	}
	t.Setenv("EAGENT_CONFIG", "")
	if cfg, err = LoadBundle(dir, "", "deepseek"); err != nil || cfg.Preset != "deepseek" || cfg.Model.Model != "deepseek-ai/DeepSeek-V4.1-Flash-1017" {
		t.Fatalf("preset as bundle: %v %+v", err, cfg.Model)
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
	if err != nil || again.Model.Model != "claude-x" || again.Description != "a copy" {
		t.Fatalf("round trip: %v %+v", err, again)
	}
	if _, err := SaveBundle(dir, "../evil", "", cfg); err == nil {
		t.Fatal("bad name accepted")
	}
}

// Every preset runs its one model at the effort its orchestrator ran at in
// the three-actor design (git main before the one-loop change; the values
// are copied from main's Presets): xhigh, or high where the model offers no
// xhigh. Fallbacks run the same effort. Each has the loop's output ceiling
// and loads and validates on its own.
func TestPresetsRunOneEffort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("EAGENT_PRESET", "")
	for _, name := range PresetNames() {
		cfg, err := Load(dir, name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if cfg.Preset != name || cfg.Summarizer.Mode != SummarizerInline {
			t.Errorf("%s: preset %q summarizer %q", name, cfg.Preset, cfg.Summarizer.Mode)
		}
		want, listed := mainOrchestratorEffort[name]
		if !listed {
			t.Errorf("%s: no effort listed from main", name)
		}
		for cur := &cfg.Model; cur != nil; cur = cur.Fallback {
			if cur.ReasoningEffort != want || cur.MaxTokens != 32768 {
				t.Errorf("%s: %s runs effort %q max_tokens %d, want %s and 32768", name, cur.Model, cur.ReasoningEffort, cur.MaxTokens, want)
			}
		}
	}
	if len(PresetNames()) != len(Presets) {
		t.Errorf("PresetNames lists %d presets, Presets has %d", len(PresetNames()), len(Presets))
	}
}

// A saved preset spells "fallback": null under model when there is none, so
// it clears a fallback the preset beneath it carries.
func TestBundleBytesSpellsOutNoFallback(t *testing.T) {
	cfg := Defaults()
	raw, err := BundleBytes("local", "", cfg)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if fb, ok := object(obj["model"])["fallback"]; !ok || string(fb) != "null" {
		t.Fatalf("model.fallback = %s (present %v) in %s", fb, ok, raw)
	}
	Presets["astra"](&cfg)
	raw, _ = BundleBytes("local", "", cfg)
	_ = json.Unmarshal(raw, &obj)
	if string(object(obj["model"])["fallback"]) == "null" {
		t.Fatalf("a real fallback was nulled: %s", raw)
	}
}

func TestSavedPresetResolvesViaPresetFlagRejectsBuiltInAndClearsFallback(t *testing.T) {
	dir := t.TempDir()
	// Project file uses openai-high (whose model carries an OpenRouter fallback).
	if err := os.MkdirAll(filepath.Dir(File(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(File(dir), []byte(`{"preset":"openai-high"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Built-in preset names and aliases cannot be overwritten by a saved preset.
	localCfg := Defaults()
	localCfg.Model = ModelRoute{Protocol: "openai-chat", BaseURL: "http://127.0.0.1:1234/v1", Model: "local/model", ReasoningEffort: "high", MaxTokens: 8192, ContextTokens: 131072}
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
	if viaPreset.Name != "my-local" || viaPreset.Model.Model != "local/model" || viaPreset.Model.Fallback != nil {
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
	if err != nil || viaEnv.Name != "my-local" || viaEnv.Model.Model != "local/model" {
		t.Fatalf("EAGENT_PRESET saved preset: %v %+v", err, viaEnv)
	}
	t.Setenv("EAGENT_PRESET", "")

	res := Resolve(dir, "my-local", "")
	if res.LoadError != "" || res.Active.Kind != "bundle" || res.Active.Name != "my-local" || res.Effective.Model.Fallback != nil {
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

// mainOrchestratorEffort is the reasoning effort of each preset's
// orchestrator on main, before the one-loop change.
var mainOrchestratorEffort = map[string]string{
	"glm": "high", "astra": "xhigh", "openai-high": "xhigh", "openai-med": "xhigh", "openai-low": "xhigh",
	"openrouter-high": "high", "openrouter-med": "high", "openrouter-low": "high",
	"anthropic-high": "xhigh", "anthropic-med": "xhigh",
	"deepinfra-high": "high", "deepinfra-med": "high", "deepinfra-low": "high",
	"fireworks-high": "high", "fireworks-med": "high", "fireworks-low": "high",
	"opencode-high": "xhigh", "opencode-med": "high", "opencode-low": "high",
	"nous-high": "high", "nous-med": "high", "nous-low": "high",
	"deepseek": "high", "qwen": "xhigh", "muse": "xhigh", "mistral": "high", "muse-open": "xhigh", "beam": "xhigh", "deepseek-1017": "high",
}

// With rollover_tokens not set, the conversation restarts at half the
// model's context window, in whole thousands: less where a small window
// with a large reply ceiling would leave the restart no room, and never
// under 20,000. No preset fixes a number.
func TestRolloverFollowsTheContextWindow(t *testing.T) {
	for _, c := range []struct{ window, maxTokens, want int }{
		{200_000, 32768, 100_000},
		{1_000_000, 32768, 500_000},
		{1_048_576, 32768, 524_000},
		{1_050_000, 32768, 525_000},
		{128_000, 32768, 62_000}, // half is 64,000; the reply and the restart need the rest
		{64_000, 32768, 20_000},
		{32_000, 4096, 20_000},
		{0, 32768, 100_000}, // an unknown window is read as 200,000
	} {
		if got := AutoRollover(c.window, c.maxTokens); got != c.want {
			t.Errorf("AutoRollover(%d, %d) = %d, want %d", c.window, c.maxTokens, got, c.want)
		}
	}
	d := Defaults()
	if d.RolloverTokens != 0 || d.Rollover() != 100_000 {
		t.Fatalf("defaults: rollover_tokens %d, restarts at %d", d.RolloverTokens, d.Rollover())
	}
	for _, name := range PresetNames() {
		c := Defaults()
		Presets[name](&c)
		if c.RolloverTokens != 0 {
			t.Errorf("%s fixes rollover_tokens at %d", name, c.RolloverTokens)
		}
		if want := c.Model.ContextTokens / 2 / 1000 * 1000; c.Rollover() != want {
			t.Errorf("%s (window %d) restarts at %d, want %d", name, c.Model.ContextTokens, c.Rollover(), want)
		}
	}
	d.RolloverTokens = 150_000
	if d.Rollover() != 150_000 {
		t.Fatalf("a configured number gave %d", d.Rollover())
	}
	// 0 is valid (it means "follow the model"); a number under 20,000 is not.
	ok, low := Defaults(), Defaults()
	low.RolloverTokens = 19_999
	has := func(c Config) bool {
		for _, p := range c.Problems() {
			if p.Path == "/rollover_tokens" && p.Severity == "error" {
				return true
			}
		}
		return false
	}
	if has(ok) || !has(low) {
		t.Fatalf("validation: 0 flagged=%v, 19999 flagged=%v", has(ok), has(low))
	}
}
