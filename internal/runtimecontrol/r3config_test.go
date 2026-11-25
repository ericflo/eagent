package runtimecontrol

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

func r3Apply(t *testing.T, project string, values Values, edits ...control.Edit) (Values, error) {
	t.Helper()
	view, err := View(project, "session", "gen", values, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := Grant(project, "session")
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: Schema, ExpectedVersion: view.Snapshot.Version, Generation: "gen", Edits: edits}
	return ApplyValues(project, values, p, view, grant)
}

func r3Env(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_PROTOCOL", "EAGENT_REASONING_EFFORT"} {
		t.Setenv(n, "")
	}
	for _, n := range []string{"OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "TOGETHER_API_KEY"} {
		t.Setenv(n, "test")
	}
}

// Switching a running session from a model at xhigh to one whose highest
// effort is lower, without naming an effort, takes the nearest effort the
// new model offers, as the project settings do (config.PickRoute), and the
// session reports the effort it then sends.
func TestSwitchingModelLiveTakesTheNearestEffortTheNewModelOffers(t *testing.T) {
	r3Env(t)
	project := t.TempDir()
	cfg, err := config.Load(project, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.ReasoningEffort != "xhigh" {
		t.Fatalf("openai-high runs at %q", cfg.Model.ReasoningEffort)
	}
	out, err := r3Apply(t, project, FromConfigFor(project, cfg),
		control.Edit{Op: "set", Key: "/routes/eagent/provider", Value: "openrouter"},
		control.Edit{Op: "set", Key: "/routes/eagent/model", Value: "z-ai/glm-5.3"})
	if err != nil {
		t.Fatalf("switch refused: %v", err)
	}
	inProject, err := config.PickRoute(project, cfg.Model, "openrouter", "z-ai/glm-5.3", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Routes[RouteEagent].Effort; got != inProject.ReasoningEffort || got != "high" {
		t.Fatalf("effort after the switch = %q; project settings give %q", got, inProject.ReasoningEffort)
	}
	// An effort named in the same change is still checked against the model.
	if _, err := r3Apply(t, project, FromConfigFor(project, cfg),
		control.Edit{Op: "set", Key: "/routes/eagent/provider", Value: "openrouter"},
		control.Edit{Op: "set", Key: "/routes/eagent/model", Value: "z-ai/glm-5.3"},
		control.Edit{Op: "set", Key: "/routes/eagent/effort", Value: "max"}); err == nil {
		t.Fatal("an effort the model does not list was accepted")
	}
}

// A route with no effort switched to a model that lists efforts reports the
// effort the switch gives it, not an empty one: the session sends what it
// reports.
func TestSwitchingModelLiveReportsTheEffortItSends(t *testing.T) {
	r3Env(t)
	project := t.TempDir()
	cfg, err := config.Load(project, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.ReasoningEffort = ""
	values := FromConfigFor(project, cfg)
	out, err := r3Apply(t, project, values,
		control.Edit{Op: "set", Key: "/routes/eagent/provider", Value: "openrouter"},
		control.Edit{Op: "set", Key: "/routes/eagent/model", Value: "z-ai/glm-5.3"})
	if err != nil {
		t.Fatal(err)
	}
	// What the session applies: the configured route with the reported pick
	// (harness routeChanges).
	sent, err := config.PickRoute(project, cfg.Model, out.Routes[RouteEagent].Provider, out.Routes[RouteEagent].Model, out.Routes[RouteEagent].Effort)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Routes[RouteEagent].Effort; got == "" || got != sent.ReasoningEffort {
		t.Fatalf("reported %q, sends %q", got, sent.ReasoningEffort)
	}
}

// Choosing a built-in preset in a running session gives the preset's model
// even when config.json names a model of its own, and the choice's
// suggestion says the same.
func TestChoosingAPresetLiveGivesItsModelOverTheFilesModel(t *testing.T) {
	r3Env(t)
	for name, file := range map[string]string{
		"a whole route": `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"OPENROUTER_API_KEY"},"tick_seconds":45}`,
		"a model alone": `{"model":{"model":"z-ai/glm-5.3"},"tick_seconds":45}`,
	} {
		project := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.File(project), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		values := FromConfigFor(project, config.Defaults())
		out, err := r3Apply(t, project, values, control.Edit{Op: "set", Key: "/config", Value: "anthropic-high"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		preset, _ := config.Load(t.TempDir(), "anthropic-high")
		if got := out.Routes[RouteEagent]; got.Model != preset.Model.Model || got.Provider != "anthropic" {
			t.Fatalf("%s: after choosing anthropic-high the route is %+v", name, got)
		}
		for _, s := range settings.SessionConfigSuggestions(project) {
			if s.Value == "anthropic-high" && s.Implies["/routes/eagent/model"] != preset.Model.Model {
				t.Fatalf("%s: the suggestion implies %v", name, s.Implies)
			}
		}
	}
}
