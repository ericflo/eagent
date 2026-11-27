package settings

import (
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
)

// Known limit (fourteenth review): a remote /preset change alone kept an
// effort the new preset's model does not offer, or left a model-only
// override pointed at the new preset's provider; e4ad2fa says it is now a
// pick and its undo knows what it wrote. Each case: the change runs a model
// the new preset's provider has at an effort it offers, and the undo loads
// as the file did before.
func TestSR2ConfigRemotePresetAloneAndItsUndo(t *testing.T) {
	relay.OfflineCatalogs()
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	for _, c := range []struct{ name, before, preset string }{
		{"null fallback", `{"preset":"openai-high","model":{"fallback":null}}`, "glm"},
		{"null fallback and effort", `{"preset":"openai-high","model":{"fallback":null,"reasoning_effort":"xhigh"}}`, "glm"},
		{"effort", `{"preset":"openai-high","model":{"reasoning_effort":"xhigh"}}`, "glm"},
		{"model", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol"}}`, "glm"},
		{"effort and model", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol","reasoning_effort":"xhigh"}}`, "glm"},
		{"unset", `{"preset":"glm","model":{"model":"zai-org/GLM-5.3-Flash","reasoning_effort":"high"}}`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			project := t.TempDir()
			writeProjectFile(t, project, c.before)
			want, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			edit := control.Edit{Op: "set", Key: "/preset", Value: c.preset}
			if c.preset == "" {
				edit = control.Edit{Op: "unset", Key: "/preset"}
			}
			raw := r11Undo(t, project, c.before, []control.Edit{edit})
			// r11Undo left the edited file in place before the undo: check it
			// by applying again.
			writeProjectFile(t, project, c.before)
			s := &Service{Project: project}
			apply(t, s, edit)
			mid, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("load after the change: %v", err)
			}
			if pr, ok := config.ProviderForURLFor(project, mid.Model.BaseURL); !ok {
				t.Errorf("after the change the model %s is at %s, which no provider knows", mid.Model.Model, mid.Model.BaseURL)
			} else if _, err := config.PickRoute(project, config.ModelRoute{}, pr.ID, mid.Model.Model, mid.Model.ReasoningEffort); err != nil {
				t.Errorf("after the change: %s at %s effort %q: %v", mid.Model.Model, pr.ID, mid.Model.ReasoningEffort, err)
			}
			writeProjectFile(t, project, raw)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("load after undo: %v\n%s", err, raw)
			}
			if got.Preset != want.Preset || got.Model.Model != want.Model.Model || got.Model.BaseURL != want.Model.BaseURL || got.Model.ReasoningEffort != want.Model.ReasoningEffort || got.Model.APIKeyEnv != want.Model.APIKeyEnv || got.Model.ContextTokens != want.Model.ContextTokens || (got.Model.Fallback == nil) != (want.Model.Fallback == nil) {
				t.Logf("fallback after undo %v, before %v", got.Model.Fallback != nil, want.Model.Fallback != nil)
				t.Errorf("after undo: preset %q %s @ %s effort %q key %s window %d; before: preset %q %s @ %s effort %q key %s window %d\nundo wrote:\n%s", got.Preset, got.Model.Model, got.Model.BaseURL, got.Model.ReasoningEffort, got.Model.APIKeyEnv, got.Model.ContextTokens, want.Preset, want.Model.Model, want.Model.BaseURL, want.Model.ReasoningEffort, want.Model.APIKeyEnv, want.Model.ContextTokens, raw)
			}
		})
	}
	_ = os.Getenv
}
