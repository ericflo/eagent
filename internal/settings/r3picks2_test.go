package settings

import (
	"context"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

const anthropicWithFallback = `{"model":{"protocol":"anthropic","base_url":"https://api.anthropic.com/v1","model":"claude-opus-5-5","api_key_env":"ANTHROPIC_API_KEY","fallback":{"protocol":"anthropic","base_url":"https://api.anthropic.com/v1","model":"claude-opus-5-5","api_key_env":"ANTHROPIC_API_KEY"}}}`

func tryApply(t *testing.T, s *Service, edits ...control.Edit) error {
	t.Helper()
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}
	_, _, err = s.Apply(context.Background(), p, s.Grant())
	return err
}

// The fallback route is a route too: moving it to another provider must not
// leave it carrying the old provider's key variable (Together would be sent
// ANTHROPIC_API_KEY's value).
func TestFallbackMovedToAnotherProviderTakesItsKey(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, anthropicWithFallback)
	s := &Service{Project: project}
	err := tryApply(t, s,
		control.Edit{Op: "set", Key: "/model/fallback/base_url", Value: "https://api.together.xyz/v1"},
		control.Edit{Op: "set", Key: "/model/fallback/model", Value: "zai-org/GLM-5.3"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	fb := cfg.Model.Fallback
	if fb == nil {
		t.Fatal("no fallback")
	}
	written, _ := os.ReadFile(config.File(project))
	if fb.BaseURL == "https://api.together.xyz/v1" && fb.APIKeyEnv != "TOGETHER_API_KEY" {
		t.Fatalf("fallback at Together reads key from %q, protocol %q\n%s", fb.APIKeyEnv, fb.Protocol, written)
	}
}

// Same with a model no list knows.
func TestFallbackMovedWithAnUnlistedModelTakesTheProvidersKey(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, anthropicWithFallback)
	s := &Service{Project: project}
	err := tryApply(t, s,
		control.Edit{Op: "set", Key: "/model/fallback/base_url", Value: "https://api.together.xyz/v1"},
		control.Edit{Op: "set", Key: "/model/fallback/model", Value: "some/unlisted"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(project, "")
	fb := cfg.Model.Fallback
	written, _ := os.ReadFile(config.File(project))
	if fb != nil && fb.BaseURL == "https://api.together.xyz/v1" && fb.APIKeyEnv != "TOGETHER_API_KEY" {
		t.Fatalf("fallback at Together reads key from %q, protocol %q\n%s", fb.APIKeyEnv, fb.Protocol, written)
	}
}

// Unsetting a route's address and model goes back to the preset's, and the
// route takes that provider's protocol and key variable: the file's
// OpenRouter key is not sent to the preset's Together address.
func TestUnsettingAddressAndModelGivesThePresetsProviderItsKey(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, `{"model":{"protocol":"openai-responses","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"OPENROUTER_API_KEY"}}`)
	s := &Service{Project: project}
	err := tryApply(t, s,
		control.Edit{Op: "unset", Key: "/model/base_url"},
		control.Edit{Op: "unset", Key: "/model/model"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(config.File(project))
	pr, ok := config.ProviderForURL(cfg.Model.BaseURL)
	if ok && cfg.Model.APIKeyEnv != pr.KeyEnv {
		t.Fatalf("route at %s (%s) reads key from %q, protocol %q\n%s", pr.ID, cfg.Model.BaseURL, cfg.Model.APIKeyEnv, cfg.Model.Protocol, written)
	}
}
