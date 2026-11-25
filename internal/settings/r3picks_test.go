package settings

import (
	"context"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// Unsetting the summarizer's route (how "use eagent's model instead" is
// sent) leaves it with no route of its own, not a part of one.
func TestUnsettingTheSummarizersRouteIsNotAModelPick(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3-Flash","api_key_env":"TOGETHER_API_KEY"}}}`)
	s := &Service{Project: project}
	var edits []control.Edit
	for _, leaf := range []string{"protocol", "base_url", "model", "api_key_env"} {
		edits = append(edits, control.Edit{Op: "unset", Key: "/summarizer/model/" + leaf})
	}
	apply(t, s, edits...)
	cfg, err := config.Load(project, "")
	if err != nil {
		written, _ := os.ReadFile(config.File(project))
		t.Fatalf("load: %v\n%s", err, written)
	}
	if cfg.Summarizer.Model != nil {
		t.Fatalf("summarizer route = %+v", *cfg.Summarizer.Model)
	}
}

// A summarizer model picked while EAGENT_MODEL and EAGENT_BASE_URL name that
// same model gets that provider's protocol, key variable and window: the
// pick is compared with the route the file builds on, not the one the
// environment makes.
func TestSummarizerPickUnderTheEnvironmentsModelGetsItsProvider(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_MODEL", "claude-opus-5-5")
	t.Setenv("EAGENT_BASE_URL", "https://api.anthropic.com/v1")
	t.Setenv("EAGENT_PROTOCOL", "anthropic")
	t.Setenv("EAGENT_API_KEY_ENV", "ANTHROPIC_API_KEY")
	project := t.TempDir()
	writeProjectFile(t, project, `{"summarizer":{"mode":"dedicated"}}`)
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{
		{Op: "set", Key: "/summarizer/model/model", Value: "claude-opus-5-5"},
		{Op: "set", Key: "/summarizer/model/base_url", Value: "https://api.anthropic.com/v1"},
	}}
	if _, _, err := s.Apply(context.Background(), p, s.Grant()); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(config.File(project))
	clearRouteEnv(t)
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatalf("load: %v\n%s", err, written)
	}
	r := cfg.Summarizer.Route(cfg.Model)
	if r.Model != "claude-opus-5-5" || r.Protocol != "anthropic" || r.APIKeyEnv != "ANTHROPIC_API_KEY" || r.ContextTokens != 1_000_000 {
		t.Fatalf("summarizer route = %+v\n%s", r, written)
	}
}

// Moving a route to another provider with a model no list knows still
// gives it that provider's protocol and key variable: the old provider's key
// is not sent to the new address.
func TestAddressSwitchWithAnUnlistedModelTakesTheNewProvidersProtocolAndKey(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, `{}`)
	s := &Service{Project: project}
	apply(t, s, control.Edit{Op: "set", Key: "/model/base_url", Value: "https://api.anthropic.com/v1"}, control.Edit{Op: "set", Key: "/model/model", Value: "claude-unlisted"})
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Protocol != "anthropic" || cfg.Model.APIKeyEnv != "ANTHROPIC_API_KEY" || cfg.Model.ContextTokens != 0 {
		t.Fatalf("route = %+v", cfg.Model)
	}

	// And to a registered keyless server.
	reg, err := config.RegisterServer(context.Background(), project, config.ProbeOptions{URL: "http://gpu-box.invalid:8000/v1", ID: "box", Protocol: "openai-chat"}, true)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, s, control.Edit{Op: "set", Key: "/model/base_url", Value: reg.Server.BaseURL}, control.Edit{Op: "set", Key: "/model/model", Value: "whatever-is-loaded"})
	if cfg, err = config.Load(project, ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Protocol != "openai-chat" || cfg.Model.APIKeyEnv != "" || cfg.Model.BaseURL != reg.Server.BaseURL {
		t.Fatalf("route = %+v", cfg.Model)
	}
}

// "default" effort (send none, the provider's own) is chosen as an effort
// of "": a remote edit can set it under a preset's effort, and it loads.
func TestRemoteEditCanChooseTheDefaultEffort(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, `{"preset":"glm"}`)
	s := &Service{Project: project}
	if cfg, _ := config.Load(project, ""); cfg.Model.ReasoningEffort == "" {
		t.Fatal("the preset sets no effort; the test needs one")
	}
	apply(t, s, control.Edit{Op: "set", Key: "/model/reasoning_effort", Value: ""})
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.ReasoningEffort != "" {
		t.Fatalf("effort = %q", cfg.Model.ReasoningEffort)
	}
}
