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

func cleanPresetEnv(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
}

// A session runs on the project's own config.json and saves its settings
// over a preset that already exists and holds the same model configured
// otherwise. The new preset has the routes the session runs, not the old
// contents of the preset it replaces.
func TestSavingOverAnotherPresetSavesTheRoutesTheSessionRuns(t *testing.T) {
	cleanPresetEnv(t)
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_KEY2", "y")
	t.Setenv("OPENROUTER_API_KEY", "z")
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	own := `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"EAGENT_TEST_KEY","context_tokens":100000,"max_tokens":8000}}`
	if err := os.WriteFile(config.File(project), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	old := `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"EAGENT_TEST_KEY2","context_tokens":64000,"max_tokens":4000}}`
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadBundle(project, "", "")
	if err != nil || cfg.Model.ContextTokens != 100_000 {
		t.Fatalf("own config: %+v %v", cfg.Model, err)
	}
	c, err := New(project, "s-own", FromConfigFor(project, cfg), func(Request, Values, map[string]any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	c.Routes = func(map[string]Route) (map[string]config.ModelRoute, error) {
		return map[string]config.ModelRoute{RouteEagent: cfg.Model}, nil
	}
	defer c.Close()
	defer runLoop(t, c)()
	view, err := View(project, "s-own", c.Generation, c.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := Grant(project, "s-own")
	prop := control.Proposal{
		Operation: "settings.apply", SchemaVersion: Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation,
		Edits: []control.Edit{
			{Op: "set", Key: "/tick_seconds", Value: float64(45)},
			{Op: "set", Key: "/save_preset", Value: "mine"},
		},
	}
	raw, _ := json.Marshal(prop)
	req := Request{ID: newID(), UserID: "u1", Proposal: prop, Digest: artifact.Digest(raw), Expires: time.Now().Add(5 * time.Second), Grant: grant}
	outcome, err := Deliver(context.Background(), project, "s-own", req, false)
	if err != nil || outcome.Status != "succeeded" {
		t.Fatalf("save: status=%s result=%v err=%v", outcome.Status, outcome.Result, err)
	}
	saved, err := config.LoadBundle(project, "", "mine")
	if err != nil {
		t.Fatal(err)
	}
	if m := saved.Model; m.APIKeyEnv != "EAGENT_TEST_KEY" || m.ContextTokens != 100_000 || m.MaxTokens != 8000 {
		t.Fatalf("saved over %q: key %s window %d reply %d; the session runs key EAGENT_TEST_KEY window 100000 reply 8000 (the old preset had EAGENT_TEST_KEY2, 64000, 4000)", "mine", m.APIKeyEnv, m.ContextTokens, m.MaxTokens)
	}
}

// A session started on the built-in preset openai-high, in a project
// whose config.json names the same model with its own key variable and
// window and no fallback, saves the preset's routes, which it runs.
func TestAPresetFromABuiltInSessionIsNotTakenFromTheProjectsSameModel(t *testing.T) {
	cleanPresetEnv(t)
	t.Setenv("OPENAI_API_KEY", "x")
	t.Setenv("OPENROUTER_API_KEY", "y")
	t.Setenv("EAGENT_TEST_KEY", "z")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	ref, err := config.ChosenPreset(t.TempDir(), "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	mine := ref.Model
	mine.APIKeyEnv, mine.ContextTokens, mine.Fallback = "EAGENT_TEST_KEY", 200_000, nil
	raw, _ := json.Marshal(map[string]any{"model": mine})
	if err := os.WriteFile(config.File(project), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ChosenPreset(project, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.APIKeyEnv != ref.Model.APIKeyEnv || cfg.Model.ContextTokens != ref.Model.ContextTokens || cfg.Model.Fallback == nil {
		t.Fatalf("the session on openai-high runs key %s window %d fallback %v; the preset has %s %d", cfg.Model.APIKeyEnv, cfg.Model.ContextTokens, cfg.Model.Fallback != nil, ref.Model.APIKeyEnv, ref.Model.ContextTokens)
	}
	values := FromConfigFor(project, cfg)
	values.Config = "bundle:copy" // as ApplyValues leaves it for /save_preset
	saved, err := ConfigFromRoutes(project, values, map[string]config.ModelRoute{RouteEagent: cfg.Model})
	if err != nil {
		t.Fatal(err)
	}
	if m := saved.Model; m.APIKeyEnv != cfg.Model.APIKeyEnv || m.ContextTokens != cfg.Model.ContextTokens || m.Fallback == nil {
		t.Fatalf("saved key %s window %d fallback %v; the session runs key %s window %d with a fallback", m.APIKeyEnv, m.ContextTokens, m.Fallback != nil, cfg.Model.APIKeyEnv, cfg.Model.ContextTokens)
	}
}
