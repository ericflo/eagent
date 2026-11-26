package runtimecontrol

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// A session started from one of the project's saved presets, whose model
// is on a local server no provider is registered for, can save a variation
// of itself (another tick, say) as a new preset, and the preset holds that
// model as the saved preset configures it.
func TestAPresetFromASessionOnASavedLocalPresetCanBeSaved(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"EAGENT_TEST_KEY"}}`
	if err := os.WriteFile(config.File(project), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"model":{"protocol":"openai-chat","base_url":"http://127.0.0.1:9/v1","model":"qwen-local","api_key_env":"EAGENT_TEST_KEY","context_tokens":32000,"reasoning_effort":"low"}}`
	if err := os.WriteFile(config.BundlePath(project, "local"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadBundle(project, "", "local")
	if err != nil || cfg.Model.Model != "qwen-local" {
		t.Fatalf("the local preset: %s %v", cfg.Model.Model, err)
	}
	values := FromConfigFor(project, cfg)
	if values.Routes[RouteEagent].Provider != "" {
		t.Fatalf("the local server has a provider: %+v", values.Routes[RouteEagent])
	}
	values.TickSeconds = 45
	saved, err := ConfigFromValues(project, values)
	if err != nil {
		t.Fatalf("the preset of a session on the saved preset %q was refused: %v", "local", err)
	}
	if saved.Model.Model != "qwen-local" || saved.Model.BaseURL != "http://127.0.0.1:9/v1" {
		t.Fatalf("saved %s @ %s", saved.Model.Model, saved.Model.BaseURL)
	}
}

// A session on a saved preset whose catalog model is configured with its
// own key variable, window, reply ceiling and fallback saves a preset with
// those, not the catalog's.
func TestAPresetFromASessionOnASavedCatalogPresetKeepsItsLimits(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_KEY2", "y")
	t.Setenv("OPENROUTER_API_KEY", "z")
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"EAGENT_TEST_KEY"}}`
	if err := os.WriteFile(config.File(project), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	other := `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"moonshotai/kimi-k3","api_key_env":"EAGENT_TEST_KEY2","context_tokens":64000,"max_tokens":4000,"reasoning_effort":"high",
		"fallback":{"protocol":"openai-chat","base_url":"http://127.0.0.1:9/v1","model":"qwen-local","api_key_env":"EAGENT_TEST_KEY"}}}`
	if err := os.WriteFile(config.BundlePath(project, "other"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadBundle(project, "", "other")
	if err != nil || cfg.Model.Model != "moonshotai/kimi-k3" {
		t.Fatalf("the preset: %s %v", cfg.Model.Model, err)
	}
	values := FromConfigFor(project, cfg)
	values.TickSeconds = 45
	saved, err := ConfigFromValues(project, values)
	if err != nil {
		t.Fatal(err)
	}
	m := saved.Model
	if m.APIKeyEnv != "EAGENT_TEST_KEY2" || m.ContextTokens != 64_000 || m.MaxTokens != 4_000 || m.Fallback == nil {
		t.Fatalf("saved %s key %s window %d reply %d fallback %v; want the running preset's EAGENT_TEST_KEY2, 64000, 4000 and its fallback", m.Model, m.APIKeyEnv, m.ContextTokens, m.MaxTokens, m.Fallback != nil)
	}
}

// Through the app: a session started on a saved preset changes its tick
// and saves itself as a new preset in one change. The change renames the
// session's configuration to the new preset's, so the routes are looked up
// in the one it was started on; the new preset has that preset's local
// model, and the tick change is applied with it.
func TestSavingAPresetFromTheAppKeepsTheRoutesOfThePresetTheSessionRuns(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"model":{"protocol":"openai-chat","base_url":"http://127.0.0.1:9/v1","model":"qwen-local","api_key_env":"EAGENT_TEST_KEY","context_tokens":32000,"max_tokens":2048,"reasoning_effort":"low"}}`
	if err := os.WriteFile(config.BundlePath(project, "local"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadBundle(project, "", "local")
	if err != nil {
		t.Fatal(err)
	}
	var applied Values
	c, err := New(project, "s-local", FromConfigFor(project, cfg), func(_ Request, v Values, _ map[string]any) error {
		applied = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer runLoop(t, c)()
	view, err := View(project, "s-local", c.Generation, c.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := Grant(project, "s-local")
	prop := control.Proposal{
		Operation: "settings.apply", SchemaVersion: Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation,
		Edits: []control.Edit{
			{Op: "set", Key: "/tick_seconds", Value: float64(45)},
			{Op: "set", Key: "/save_preset", Value: "local-copy"},
		},
	}
	raw, _ := json.Marshal(prop)
	req := Request{ID: newID(), UserID: "u1", Proposal: prop, Digest: artifact.Digest(raw), Expires: time.Now().Add(5 * time.Second), Grant: grant}
	outcome, err := Deliver(context.Background(), project, "s-local", req, false)
	if err != nil || outcome.Status != "succeeded" {
		t.Fatalf("save: status=%s result=%v err=%v", outcome.Status, outcome.Result, err)
	}
	if applied.TickSeconds != 45 {
		t.Fatalf("the tick change was not applied: %+v", applied)
	}
	saved, err := config.Load(project, "local-copy")
	if err != nil {
		t.Fatal(err)
	}
	m := saved.Model
	if m.Model != "qwen-local" || m.BaseURL != "http://127.0.0.1:9/v1" || m.APIKeyEnv != "EAGENT_TEST_KEY" || m.ContextTokens != 32_000 || m.MaxTokens != 2048 || saved.TickSeconds != 45 {
		t.Fatalf("saved %s @ %s key %s window %d reply %d tick %d", m.Model, m.BaseURL, m.APIKeyEnv, m.ContextTokens, m.MaxTokens, saved.TickSeconds)
	}
}

// A session started on a built-in preset with a fallback (openai-high:
// OpenAI, then the same model through OpenRouter) saves what it runs: on
// the first route, that route with its fallback; on the fallback, the
// fallback as the preset configures it.
func TestAPresetFromASessionOnABuiltInPresetKeepsItsRoutes(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
	t.Setenv("OPENAI_API_KEY", "x")
	t.Setenv("OPENROUTER_API_KEY", "y")
	project := t.TempDir()
	cfg, err := config.ChosenPreset(project, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	primary, fallback := cfg.Model, *cfg.Model.Fallback
	values := FromConfigFor(project, cfg)
	if values.Config != "openai-high" {
		t.Fatalf("the session runs %q", values.Config)
	}
	saved, err := ConfigFromValues(project, values)
	if err != nil {
		t.Fatal(err)
	}
	if m := saved.Model; m.BaseURL != primary.BaseURL || m.Model != primary.Model || m.ContextTokens != primary.ContextTokens || m.Fallback == nil || m.Fallback.Model != fallback.Model {
		t.Fatalf("on the first route it saved %s @ %s window %d fallback %v", m.Model, m.BaseURL, m.ContextTokens, m.Fallback)
	}
	values.Routes[RouteEagent] = routeFor(project, fallback)
	saved, err = ConfigFromValues(project, values)
	if err != nil {
		t.Fatal(err)
	}
	if m := saved.Model; m.BaseURL != fallback.BaseURL || m.Model != fallback.Model || m.APIKeyEnv != fallback.APIKeyEnv || m.ContextTokens != fallback.ContextTokens || m.MaxTokens != fallback.MaxTokens {
		t.Fatalf("on the fallback it saved %s @ %s key %s window %d reply %d; the preset has %s @ %s key %s window %d reply %d",
			m.Model, m.BaseURL, m.APIKeyEnv, m.ContextTokens, m.MaxTokens, fallback.Model, fallback.BaseURL, fallback.APIKeyEnv, fallback.ContextTokens, fallback.MaxTokens)
	}
}
