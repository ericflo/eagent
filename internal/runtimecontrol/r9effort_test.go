package runtimecontrol

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

// With a project file written for openai-high at xhigh, the settings
// suggestion for "glm" implies the effort GLM-5.3 runs at when chosen, and
// choosing it with what it implies is accepted.
func TestR9GLMSuggestionImpliesAnEffortTheSwitchTakes(t *testing.T) {
	for name, raw := range map[string]string{
		"full route":  `{"preset":"openai-high","model":{"protocol":"openai-responses","base_url":"https://api.openai.com/v1","model":"gpt-6.2-astra","reasoning_effort":"xhigh"}}`,
		"effort only": `{"preset":"openai-high","model":{"reasoning_effort":"xhigh"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			r3Env(t)
			project := t.TempDir()
			if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config.File(project), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			var glm *control.Suggestion
			for _, s := range settings.SessionConfigSuggestions(project, true) {
				if s.Value == "glm" {
					glm = &s
				}
			}
			if glm == nil {
				t.Fatal("no glm suggestion")
			}
			cfg, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			edits := []control.Edit{{Op: "set", Key: "/config", Value: "glm"}}
			for k, v := range glm.Implies {
				edits = append(edits, control.Edit{Op: "set", Key: k, Value: v})
			}
			out, err := r3Apply(t, project, FromConfigFor(project, cfg), edits...)
			if err != nil {
				t.Fatalf("/config = glm with what it implies refused: %v", err)
			}
			if rt := out.Routes[RouteEagent]; rt.Model != "zai-org/GLM-5.3" || rt.Effort != "high" {
				t.Fatalf("route after the switch = %+v", rt)
			}
			if got := glm.Implies["/routes/eagent/effort"]; got != "high" {
				t.Fatalf("glm implies effort %v (%v)", got, glm.Implies)
			}
		})
	}
}
