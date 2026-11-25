package config

import (
	"context"
	"os"
	"testing"
)

// unsetEnv removes variables for the test and puts them back after it, so a
// test does not depend on what the shell running it has set.
func unsetEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
}

// routeEnvNames are every variable that sets a field of the main or the
// summarizer's route.
func routeEnvNames() []string {
	var out []string
	for suffix := range routeEnvFields {
		out = append(out, "EAGENT_"+suffix, "EAGENT_ORCHESTRATOR_"+suffix, "EAGENT_SUMMARIZER_"+suffix)
	}
	return append(out, "EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE")
}

func writeFile(t *testing.T, project, raw string) {
	t.Helper()
	if err := os.MkdirAll(project+"/.agents/eagent", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(File(project), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A route's context window is its model's: a layer that changes the model
// without naming a window gets the new model's, or 0 (unknown), never the
// window of the model before.
func TestContextTokensFollowTheModel(t *testing.T) {
	unsetEnv(t, routeEnvNames()...)
	for _, c := range []struct {
		name, preset, file string
		env                map[string]string
		want               int
	}{
		{"file names a known model", "openrouter-high", `{"model":{"model":"z-ai/glm-5.3"}}`, nil, 200_000},
		{"file names an unknown model", "openrouter-high", `{"model":{"model":"made/up"}}`, nil, 0},
		{"file names a bigger model over the defaults", "", `{"model":{"model":"deepseek-ai/DeepSeek-V4.1-Flash-1017"}}`, nil, 1_048_576},
		{"file names the window too", "openrouter-high", `{"model":{"model":"z-ai/glm-5.3","context_tokens":123456}}`, nil, 123456},
		{"file restates the preset's model", "openrouter-high", `{"model":{"model":"moonshotai/kimi-k3"}}`, nil, 1_000_000},
		{"a null window is no window", "openrouter-high", `{"model":{"model":"made/up","context_tokens":null}}`, nil, 0},
		{"env names an unknown model", "openrouter-high", `{}`, map[string]string{"EAGENT_MODEL": "made/up"}, 0},
		{"env model after a file window", "openrouter-high", `{"model":{"context_tokens":500000}}`, map[string]string{"EAGENT_MODEL": "z-ai/glm-5.3"}, 200_000},
	} {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			project := t.TempDir()
			writeFile(t, project, c.file)
			cfg, err := Load(project, c.preset)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Model.ContextTokens != c.want {
				t.Fatalf("context_tokens = %d, want %d", cfg.Model.ContextTokens, c.want)
			}
			res := Resolve(project, c.preset, "")
			if res.Effective.Model.ContextTokens != c.want {
				t.Fatalf("Resolve: context_tokens = %d, want %d", res.Effective.Model.ContextTokens, c.want)
			}
		})
	}
}

// A fallback route's window follows its own model too.
func TestFallbackContextTokensFollowTheModel(t *testing.T) {
	unsetEnv(t, routeEnvNames()...)
	project := t.TempDir()
	writeFile(t, project, `{"model":{"fallback":{"model":"openai/gpt-6-luna-x"}}}`)
	cfg, err := Load(project, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.ContextTokens != 1_050_000 || cfg.Model.Fallback == nil || cfg.Model.Fallback.ContextTokens != 0 {
		t.Fatalf("routes = %+v / %+v", cfg.Model, cfg.Model.Fallback)
	}
}

// A pick of a model whose window no list gives leaves the window unknown.
func TestPickRouteUnknownWindowIsZero(t *testing.T) {
	project := t.TempDir()
	if _, err := RegisterServer(context.Background(), project, ProbeOptions{URL: "http://127.0.0.1:9/v1", ID: "bare", Label: "Bare"}, true); err != nil {
		t.Fatal(err)
	}
	from := Defaults()
	Presets["openai-high"](&from)
	picked, err := PickRoute(project, from.Model, "bare", "some-local-model", "")
	if err != nil {
		t.Fatal(err)
	}
	if picked.ContextTokens != 0 {
		t.Fatalf("context_tokens = %d, want 0 (unknown), not the old model's", picked.ContextTokens)
	}
	if got := KnownContext(project, "https://openrouter.ai/api/v1", "z-ai/glm-5.3"); got != 200_000 {
		t.Fatalf("KnownContext = %d", got)
	}
}

// Any EAGENT_SUMMARIZER_* route variable applies, not only the model's:
// an effort alone puts a dedicated summarizer on the main model at it.
func TestSummarizerEffortEnvAlone(t *testing.T) {
	unsetEnv(t, routeEnvNames()...)
	t.Setenv("EAGENT_SUMMARIZER_MODE", "dedicated")
	t.Setenv("EAGENT_SUMMARIZER_REASONING_EFFORT", "none")
	project := t.TempDir()
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Summarizer.Route(cfg.Model)
	if r.ReasoningEffort != "none" || r.Model != cfg.Model.Model || r.BaseURL != cfg.Model.BaseURL {
		t.Fatalf("summarizer route = %+v", r)
	}
	if cfg.Model.ReasoningEffort == "none" {
		t.Fatal("the main model's effort changed")
	}
	res := Resolve(project, "", "")
	if res.Sources["/summarizer/model/reasoning_effort"] != "env:EAGENT_SUMMARIZER_REASONING_EFFORT" {
		t.Fatalf("source = %q", res.Sources["/summarizer/model/reasoning_effort"])
	}
}

// An inline summarizer calls no route, so a broken one left written does
// not stop a load.
func TestInlineSummarizerRouteIsNotValidated(t *testing.T) {
	unsetEnv(t, routeEnvNames()...)
	project := t.TempDir()
	writeFile(t, project, `{"summarizer":{"mode":"inline","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1"}}}`)
	if _, err := Load(project, ""); err != nil {
		t.Fatalf("inline load: %v", err)
	}
	writeFile(t, project, `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1"}}}`)
	if _, err := Load(project, ""); err == nil {
		t.Fatal("a dedicated summarizer with no model loaded")
	}
}

// The editor leaves an effort of "" (send none) as a null under a preset
// that sets one; it loads as "", not as the preset's effort.
func TestNullEffortClearsThePresets(t *testing.T) {
	unsetEnv(t, routeEnvNames()...)
	project := t.TempDir()
	cfg := Defaults()
	Presets["openrouter-high"](&cfg)
	cfg.Model.ReasoningEffort = ""
	edits, err := Overlay(cfg, "openrouter-high")
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
	if loaded.Model.ReasoningEffort != "" || loaded.Model.Model != cfg.Model.Model {
		t.Fatalf("effort = %q, model %q", loaded.Model.ReasoningEffort, loaded.Model.Model)
	}
}
