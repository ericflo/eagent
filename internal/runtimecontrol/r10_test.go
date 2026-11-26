package runtimecontrol

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

func r10Write(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func r10Suggestion(t *testing.T, project, value string) control.Suggestion {
	t.Helper()
	for _, s := range settings.SessionConfigSuggestions(project, false) {
		if s.Value == value {
			return s
		}
	}
	t.Fatalf("no %s suggestion", value)
	return control.Suggestion{}
}

// Finding 20: a saved preset that puts the session on GLM-5.3 under a
// project file at xhigh implies the effort the switch takes (the nearest
// GLM-5.3 offers), and choosing it with what it implies is accepted, as
// for a built-in preset (TestR9GLMSuggestionImpliesAnEffortTheSwitchTakes).
func TestR10BundleSuggestionImpliesAnEffortTheSwitchTakes(t *testing.T) {
	r3Env(t)
	project := t.TempDir()
	r10Write(t, config.File(project), `{"preset":"openai-high","model":{"reasoning_effort":"xhigh"}}`)
	r10Write(t, config.BundlePath(project, "fast-glm"), `{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}`)
	s := r10Suggestion(t, project, "bundle:fast-glm")
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	edits := []control.Edit{{Op: "set", Key: "/config", Value: "bundle:fast-glm"}}
	for k, v := range s.Implies {
		edits = append(edits, control.Edit{Op: "set", Key: k, Value: v})
	}
	out, err := r3Apply(t, project, FromConfigFor(project, cfg), edits...)
	if err != nil {
		t.Fatalf("/config = bundle:fast-glm with what it implies refused: %v (implies %v)", err, s.Implies)
	}
	if rt := out.Routes[RouteEagent]; rt.Model != "zai-org/GLM-5.3" || rt.Effort != "high" {
		t.Fatalf("route after the switch = %+v", rt)
	}
	if got := s.Implies["/routes/eagent/effort"]; got != "high" {
		t.Fatalf("bundle:fast-glm implies effort %v (%v)", got, s.Implies)
	}
}
