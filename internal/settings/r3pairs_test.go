package settings

import (
	"context"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// A remote edit that names an address and another provider's key variable
// together is refused: ANTHROPIC_API_KEY is never sent to Together.
func TestRemoteEditCannotPairAProvidersKeyWithAnotherProvider(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, anthropicWithFallback)
	s := &Service{Project: project}
	err := tryApply(t, s,
		control.Edit{Op: "set", Key: "/model/base_url", Value: "https://api.together.xyz/v1"},
		control.Edit{Op: "set", Key: "/model/model", Value: "zai-org/GLM-5.3"},
		control.Edit{Op: "set", Key: "/model/protocol", Value: "openai-chat"},
		control.Edit{Op: "set", Key: "/model/api_key_env", Value: "ANTHROPIC_API_KEY"})
	if err != nil {
		t.Logf("refused: %v", err)
		return
	}
	cfg, _ := config.Load(project, "")
	if cfg.Model.BaseURL == "https://api.together.xyz/v1" && cfg.Model.APIKeyEnv == "ANTHROPIC_API_KEY" {
		t.Fatalf("remote edit paired ANTHROPIC_API_KEY with Together: %+v", cfg.Model)
	}
}

// Nor in two edits: the address first (a proper pick), the key next.
func TestRemoteKeyChangeAfterAnAddressChangeIsRefused(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, anthropicWithFallback)
	s := &Service{Project: project}
	if err := tryApply(t, s,
		control.Edit{Op: "set", Key: "/model/base_url", Value: "https://api.together.xyz/v1"},
		control.Edit{Op: "set", Key: "/model/model", Value: "zai-org/GLM-5.3"}); err != nil {
		t.Fatal(err)
	}
	err := tryApply(t, s, control.Edit{Op: "set", Key: "/model/api_key_env", Value: "ANTHROPIC_API_KEY"})
	if err != nil {
		t.Logf("refused: %v", err)
		return
	}
	cfg, _ := config.Load(project, "")
	if cfg.Model.BaseURL == "https://api.together.xyz/v1" && cfg.Model.APIKeyEnv == "ANTHROPIC_API_KEY" {
		t.Fatalf("remote edit paired ANTHROPIC_API_KEY with Together: %+v", cfg.Model)
	}
}

// While EAGENT_API_KEY_ENV pins the key variable, the file's key variable
// does not apply: a remote move of the address would send the pinned key
// to the new provider, and is refused.
func TestRemoteAddressSwitchUnderAnEnvPinnedKeyIsRefused(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_API_KEY_ENV", "ANTHROPIC_API_KEY")
	project := t.TempDir()
	writeProjectFile(t, project, anthropicWithFallback)
	s := &Service{Project: project}
	err := tryApply(t, s,
		control.Edit{Op: "set", Key: "/model/base_url", Value: "https://api.together.xyz/v1"},
		control.Edit{Op: "set", Key: "/model/model", Value: "zai-org/GLM-5.3"})
	if err != nil {
		t.Logf("refused: %v", err)
		return
	}
	cfg, _ := config.Load(project, "")
	if cfg.Model.BaseURL == "https://api.together.xyz/v1" && cfg.Model.APIKeyEnv == "ANTHROPIC_API_KEY" {
		t.Fatalf("effective route sends ANTHROPIC_API_KEY to Together: %+v", cfg.Model)
	}
}

// What a remote edit may still do with keys and addresses.
func TestRemoteEditsThatKeepKeysWithTheirProvidersStillApply(t *testing.T) {
	t.Run("an edit that leaves a hand-written pairing alone", func(t *testing.T) {
		clearRouteEnv(t)
		project := t.TempDir()
		// The local user paired these; a remote edit of something else keeps it.
		writeProjectFile(t, project, `{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"OPENROUTER_API_KEY"}}`)
		apply(t, &Service{Project: project}, control.Edit{Op: "set", Key: "/max_calls_per_turn", Value: float64(4)})
	})
	t.Run("an edit while the environment pins a key", func(t *testing.T) {
		clearRouteEnv(t)
		t.Setenv("EAGENT_API_KEY_ENV", "ANTHROPIC_API_KEY")
		project := t.TempDir()
		writeProjectFile(t, project, `{"preset":"glm"}`)
		apply(t, &Service{Project: project}, control.Edit{Op: "set", Key: "/max_calls_per_turn", Value: float64(4)})
	})
	t.Run("a pick at another provider, its fallback too", func(t *testing.T) {
		clearRouteEnv(t)
		project := t.TempDir()
		writeProjectFile(t, project, anthropicWithFallback)
		s := &Service{Project: project}
		apply(t, s, control.Edit{Op: "set", Key: "/model/base_url", Value: "https://api.together.xyz/v1"}, control.Edit{Op: "set", Key: "/model/model", Value: "zai-org/GLM-5.3"})
		apply(t, s, control.Edit{Op: "set", Key: "/model/fallback/base_url", Value: "https://openrouter.ai/api/v1"}, control.Edit{Op: "set", Key: "/model/fallback/model", Value: "z-ai/glm-5.3"})
		cfg, err := config.Load(project, "")
		if err != nil || cfg.Model.APIKeyEnv != "TOGETHER_API_KEY" || cfg.Model.Fallback == nil || cfg.Model.Fallback.APIKeyEnv != "OPENROUTER_API_KEY" {
			t.Fatalf("%+v %v", cfg.Model, err)
		}
	})
	t.Run("no key, then the provider's own", func(t *testing.T) {
		clearRouteEnv(t)
		project := t.TempDir()
		writeProjectFile(t, project, `{"preset":"glm"}`)
		s := &Service{Project: project}
		apply(t, s, control.Edit{Op: "set", Key: "/model/api_key_env", Value: ""})
		apply(t, s, control.Edit{Op: "set", Key: "/model/api_key_env", Value: "TOGETHER_API_KEY"})
	})
}

// A bundle saved from a connector can become the default, so it is held to
// the same pairing: the form as it is saves, another provider's key does not.
func TestRemoteBundleSaveKeepsKeysWithTheirProviders(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, anthropicWithFallback)
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	editor, err := config.LockEditor(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	defer editor.Close()
	p := control.Proposal{Operation: "bundle.save", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Parameters: map[string]any{"name": "same", "config_json": anthropicWithFallback}}
	if _, err := s.PrepareResource(editor, p, s.Grant()); err != nil {
		t.Fatalf("the configuration as it is: %v", err)
	}
	p.Parameters = map[string]any{"name": "crossed", "config_json": `{"model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"ANTHROPIC_API_KEY"}}`}
	if _, err := s.PrepareResource(editor, p, s.Grant()); err == nil {
		t.Fatal("a bundle pairing ANTHROPIC_API_KEY with Together was accepted")
	}
}
