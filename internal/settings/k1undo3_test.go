package settings

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// rv15c3Undo computes and prepares the undo of a command journaled as
// (edits, before, desired) with s, against the file on disk now.
func rv15c3Undo(t *testing.T, s *Service, edits []control.Edit, before, desired string, inForce [][2]string) (string, []control.Edit, error) {
	t.Helper()
	inverse, err := ConfigInverse(control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: "earlier", Edits: edits}, before, desired)
	if err != nil {
		t.Fatal(err)
	}
	now, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, now.Snapshot.Version
	raw, _, _, err := s.PrepareUndo(inverse, before, desired, s.Grant(), inForce...)
	return raw, inverse.Edits, err
}

// The preset the file builds on is a layer (the session's), not the file's.
// A remote edit replaces the file's own key variable; the layer then moves
// to another provider; the undo puts the old key variable back. The route
// that results pairs an OpenAI key variable with Anthropic's address, which
// the settings refuse from a person (and refused from this undo at
// f36e19c).
func TestRV15C3UndoAfterALayerChangeSendsAnOldKeyToANewAddress(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			project := t.TempDir()
			before := `{"model":{"api_key_env":"OPENAI_API_KEY_WORK"}}`
			edits := []control.Edit{{Op: "set", Key: "/model/api_key_env", Value: "OPENAI_API_KEY"}}
			writeProjectFile(t, project, before)
			s := &Service{Project: project, Preset: "openai-high"}
			if cfg := config.Resolve(project, s.Preset, s.Bundle).Effective; !strings.Contains(cfg.Model.BaseURL, "openai.com") {
				t.Fatalf("setup: the layer is not OpenAI's: %+v", cfg.Model)
			}
			// What the journal records as in force at the command.
			inForce := RoutePairs(config.Resolve(project, s.Preset, s.Bundle).Effective)
			apply(t, s, edits...)
			wrote, _ := os.ReadFile(config.File(project))
			// The same file, now over another provider's preset.
			s2 := &Service{Project: project, Preset: "anthropic-med"}
			// A person's proposal of the same value is refused.
			if err := tryApply(t, s2, control.Edit{Op: "set", Key: "/model/api_key_env", Value: "OPENAI_API_KEY_WORK"}); err == nil {
				t.Fatalf("setup: a person's proposal pairing OPENAI_API_KEY_WORK with Anthropic was accepted")
			}
			writeProjectFile(t, project, string(wrote))
			raw, inv, err := rv15c3Undo(t, s2, edits, before, string(wrote), inForce)
			if err != nil {
				t.Logf("undo refused: %v (acceptable)", err)
				return
			}
			writeProjectFile(t, project, raw)
			got := config.Resolve(project, s2.Preset, s2.Bundle).Effective
			if got.Model.APIKeyEnv == "OPENAI_API_KEY_WORK" && !strings.Contains(got.Model.BaseURL, "openai.com") {
				t.Fatalf("undo accepted and sends OPENAI_API_KEY_WORK to %s (%s)\nreversal: %+v\nundo wrote:\n%s", got.Model.BaseURL, got.Model.Model, inv, raw)
			}
		})
	}
}

// The same with the layer a connector's Service sees: EAGENT_PRESET, which
// eagent's shell sets, moved from openai-high to anthropic-med between the
// command and the undo.
func TestRV15C3UndoAfterAnEnvPresetChange(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			t.Setenv("EAGENT_PRESET", "openai-high")
			project := t.TempDir()
			before := `{"model":{"api_key_env":"OPENAI_API_KEY_WORK"}}`
			edits := []control.Edit{{Op: "set", Key: "/model/api_key_env", Value: "OPENAI_API_KEY"}}
			writeProjectFile(t, project, before)
			s := &Service{Project: project}
			if cfg := config.Resolve(project, "", "").Effective; !strings.Contains(cfg.Model.BaseURL, "openai.com") {
				t.Fatalf("setup: the layer is not OpenAI's: %+v", cfg.Model)
			}
			// What the journal records as in force at the command.
			inForce := RoutePairs(config.Resolve(project, "", "").Effective)
			apply(t, s, edits...)
			wrote, _ := os.ReadFile(config.File(project))
			t.Setenv("EAGENT_PRESET", "anthropic-med")
			raw, inv, err := rv15c3Undo(t, s, edits, before, string(wrote), inForce)
			if err != nil {
				t.Logf("undo refused: %v (acceptable)", err)
				return
			}
			writeProjectFile(t, project, raw)
			got := config.Resolve(project, "", "").Effective
			if got.Model.APIKeyEnv == "OPENAI_API_KEY_WORK" && !strings.Contains(got.Model.BaseURL, "openai.com") {
				t.Fatalf("undo accepted and sends OPENAI_API_KEY_WORK to %s (%s)\nreversal: %+v\nundo wrote:\n%s", got.Model.BaseURL, got.Model.Model, inv, raw)
			}
		})
	}
}

// A remote pick of a model the file did not name, then a local edit of the
// route's address: the undo takes the model out again (the reversal puts no
// model back) and goes through at f36e19c, keeping the local address.
func TestRV15C3UndoUnsettingAModelAfterALocalAddressEdit(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			project := t.TempDir()
			before := `{"preset":"openai-high"}`
			edits := []control.Edit{{Op: "set", Key: "/model/model", Value: "gpt-6.1-sol"}}
			writeProjectFile(t, project, before)
			s := &Service{Project: project}
			inForce := RoutePairs(config.Resolve(project, s.Preset, s.Bundle).Effective)
			apply(t, s, edits...)
			wrote, _ := os.ReadFile(config.File(project))
			local := strings.Replace(string(wrote), `"model": {`, `"model": {"base_url":"https://api.openai.com/v1/",`, 1)
			if local == string(wrote) {
				t.Fatalf("setup:\n%s", wrote)
			}
			writeProjectFile(t, project, local)
			raw, inv, err := rv15c3Undo(t, s, edits, before, string(wrote), inForce)
			var conflict *config.ErrConflict
			if errors.As(err, &conflict) {
				t.Fatalf("undo conflicts on a local address edit though it puts no model back\nreversal: %+v\ncommand wrote:\n%s\nlocal:\n%s", inv, wrote, local)
			}
			if err != nil {
				t.Fatalf("undo refused: %v\nreversal: %+v", err, inv)
			}
			writeProjectFile(t, project, raw)
			if _, err := config.Load(project, ""); err != nil {
				t.Fatalf("load: %v\n%s", err, raw)
			}
		})
	}
}
