package runtimecontrol

import (
	"context"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// E7: since 289a0c2 the connector (Deliver) leaves a live route edit's
// key variable to the session. Each route edit whose key is not set where
// the session runs comes back through the connector as the session's
// refusal, "rejected" with the variable named, and nothing is applied: a
// catalog provider whose usual variable is unset, a chosen preset whose
// provider's variable is unset, and the dedicated summarizer's route.
func TestK1ASessionsKeyRefusalComesBackThroughTheConnector(t *testing.T) {
	r14Env(t)
	glm := func(t *testing.T, project, session string, applied *[]Values) *Controller {
		t.Setenv("TOGETHER_API_KEY", "x")
		cfg := config.Defaults()
		cfg.Summarizer = config.Summarizer{Mode: config.SummarizerDedicated, Model: &config.ModelRoute{Protocol: "openai-chat", BaseURL: "https://api.together.xyz/v1", Model: "zai-org/GLM-5.3", APIKeyEnv: "TOGETHER_API_KEY"}}
		return r14Session(t, project, session, cfg, applied)
	}
	for _, tc := range []struct {
		name  string
		unset string
		edits []control.Edit
	}{
		{"a catalog provider", "OPENROUTER_API_KEY", []control.Edit{{Op: "set", Key: "/routes/eagent/provider", Value: "openrouter"}, {Op: "set", Key: "/routes/eagent/model", Value: "z-ai/glm-5.3"}}},
		{"a chosen preset", "ANTHROPIC_API_KEY", []control.Edit{{Op: "set", Key: "/config", Value: "anthropic-high"}}},
		{"the summarizer", "OPENROUTER_API_KEY", []control.Edit{{Op: "set", Key: "/routes/summarizer/provider", Value: "openrouter"}, {Op: "set", Key: "/routes/summarizer/model", Value: "z-ai/glm-5.3"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.unset, "")
			project := t.TempDir()
			var applied []Values
			c := glm(t, project, "s", &applied)
			q := r14Request(t, c, tc.edits...)
			stop := runLoop(t, c)
			out, err := Deliver(context.Background(), project, "s", q, false)
			stop()
			msg, _ := out.Result["message"].(string)
			if err != nil || out.Status != "rejected" || !strings.Contains(msg, tc.unset+" is not set") || len(applied) != 0 {
				t.Fatalf("through the connector: %s %q (%v), applied %+v", out.Status, msg, err, applied)
			}
		})
	}
}
