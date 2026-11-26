package runtimecontrol

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

// Finding 27: choosing a built-in preset (whose summarizer is inline) in a
// session with a dedicated summarizer leaves that summarizer where it is,
// as the choice's suggestion says.
func TestChoosingAnInlinePresetLiveKeepsADedicatedSummarizer(t *testing.T) {
	r3Env(t)
	project := t.TempDir()
	cfg, err := config.Load(project, "glm")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Summarizer = config.Summarizer{Mode: config.SummarizerDedicated, Model: &config.ModelRoute{
		Protocol: "openai-chat", BaseURL: "https://openrouter.ai/api/v1", Model: "z-ai/glm-5.3-flash", APIKeyEnv: "OPENROUTER_API_KEY",
	}}
	values := FromConfigFor(project, cfg)
	was := values.Routes[RouteSummarizer]
	if was.Model != "z-ai/glm-5.3-flash" {
		t.Fatalf("setup: summarizer route %+v", was)
	}
	out, err := r3Apply(t, project, values, control.Edit{Op: "set", Key: "/config", Value: "anthropic-high"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range settings.SessionConfigSuggestions(project) {
		if s.Value != "anthropic-high" {
			continue
		}
		if _, says := s.Implies["/routes/summarizer/model"]; says {
			t.Fatalf("setup: the suggestion now implies a summarizer: %v", s.Implies)
		}
	}
	if got := out.Routes[RouteSummarizer]; got != was {
		t.Fatalf("summarizer after choosing anthropic-high = %+v, was %+v", got, was)
	}
}
