//go:build !rv16old

package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// rv17Setup applies one command to a project holding before and returns
// the service, grant, command, the file it wrote and the pairs in force.
func rv17Setup(t *testing.T, before string, edits []control.Edit) (*Service, control.Grant, control.Proposal, string, [][2]string) {
	t.Helper()
	project := t.TempDir()
	writeProjectFile(t, project, before)
	s := &Service{Project: project}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	inForce := RoutePairs(config.Resolve(project, s.Preset, s.Bundle).Effective)
	cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}
	if _, _, err := s.Apply(context.Background(), cmd, g); err != nil {
		t.Fatalf("apply: %v", err)
	}
	desired, _ := os.ReadFile(config.File(project))
	return s, g, cmd, string(desired), inForce
}

func rv17Local(t *testing.T, s *Service, edit func(map[string]any)) {
	t.Helper()
	raw, _ := os.ReadFile(config.File(s.Project))
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	edit(m)
	out, _ := json.Marshal(m)
	writeProjectFile(t, s.Project, string(out))
}

// A section the file had written as null (here the summarizer) that a
// command wrote a field into is undone as at f36e19c, where the undo unset
// the field.
func TestRV17NullSectionUndo(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			before := `{"preset":"openai-high","summarizer":null}`
			s, g, cmd, desired, inForce := rv17Setup(t, before, []control.Edit{{Op: "set", Key: "/summarizer/mode", Value: config.SummarizerDedicated}})
			inverse, raw, _, _, err := rv16Undo(t, s, g, cmd, before, desired, inForce)
			if err != nil {
				t.Fatalf("undo: %v (reversal %+v)", err, inverse.Edits)
			}
			writeProjectFile(t, s.Project, raw)
			got, err := config.Load(s.Project, "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Summarizer.Mode == config.SummarizerDedicated {
				t.Fatalf("after undo the summarizer is still dedicated:\n%s", raw)
			}
		})
	}
}

// Putting a preset back moves an address-less route's address: with the
// model changed by hand since, it conflicts as an address put back does.
func TestRV17PresetPutBackUnderLocalModel(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			before := `{"preset":"openai-high"}`
			s, g, cmd, desired, inForce := rv17Setup(t, before, []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-high"}})
			rv17Local(t, s, func(m map[string]any) {
				m["model"] = map[string]any{"model": "claude-opus-5-5"}
			})
			inverse, raw, _, _, err := rv16Undo(t, s, g, cmd, before, desired, inForce)
			var conflict *config.ErrConflict
			if !errors.As(err, &conflict) {
				writeProjectFile(t, s.Project, raw)
				got, _ := config.Load(s.Project, "")
				t.Fatalf("undo: %v, want a conflict (reversal %+v); it would run %s at %s", err, inverse.Edits, got.Model.Model, got.Model.BaseURL)
			}
		})
	}
}

// The same with the model the person writes by hand being the new preset's
// own: the route resolves now as the command left it, but putting the preset
// back would run that model at OpenAI's address, which it never was before
// the command, so it conflicts too.
func TestK3PresetPutBackUnderTheNewPresetsOwnModel(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			before := `{"preset":"openai-high"}`
			s, g, cmd, desired, inForce := rv17Setup(t, before, []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-high"}})
			rv17Local(t, s, func(m map[string]any) {
				m["model"] = map[string]any{"model": "claude-fable-5-1"}
			})
			inverse, _, _, _, err := rv16Undo(t, s, g, cmd, before, desired, inForce)
			var conflict *config.ErrConflict
			if !errors.As(err, &conflict) {
				t.Fatalf("undo: %v, want a conflict (reversal %+v)", err, inverse.Edits)
			}
		})
	}
}

// When the file the command wrote no longer loads over today's layers (here
// a dedicated summarizer, set by the environment since, makes an inline
// route's second fallback an error), an undo that changes a route cannot
// tell where the model would go: it is refused, saying so.
func TestK3UndoRefusedWhenTheCommandsFileNoLongerLoads(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	route := `"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3-Flash","api_key_env":"TOGETHER_API_KEY"`
	before := `{"preset":"openai-high","summarizer":{"mode":"inline","model":{` + route + `,"fallback":{` + route + `,"fallback":{` + route + `}}}}}`
	s, g, cmd, desired, inForce := rv17Setup(t, before, []control.Edit{{Op: "set", Key: "/model/model", Value: "gpt-6.1-sol"}})
	rv17Local(t, s, func(m map[string]any) {
		delete(m["summarizer"].(map[string]any)["model"].(map[string]any)["fallback"].(map[string]any), "fallback")
	})
	t.Setenv("EAGENT_SUMMARIZER_MODE", "dedicated")
	_, raw, _, _, err := rv16Undo(t, s, g, cmd, before, desired, inForce)
	if err == nil || !strings.Contains(err.Error(), "no longer load") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("undo: %v, want it refused saying the command's settings no longer load\nwrote:\n%s", err, raw)
	}
}

// An undo of an undo that sets a fallback's model back, after the person
// gave the fallback the undo turned off an address of its own, conflicts:
// the model would go to that address.
func TestRV17RedoFallbackModelAtLocalAddress(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			before := `{"preset":"openai-high","model":{"fallback":null}}`
			s, g, cmd, desired, inForce := rv17Setup(t, before, []control.Edit{{Op: "set", Key: "/model/fallback/model", Value: "openai/gpt-6.1-sol"}})
			inverse, raw, undoBefore, undoInForce, err := rv16Undo(t, s, g, cmd, before, desired, inForce)
			if err != nil {
				t.Fatalf("undo: %v", err)
			}
			writeProjectFile(t, s.Project, raw)
			rv17Local(t, s, func(m map[string]any) {
				m["model"].(map[string]any)["fallback"] = map[string]any{"base_url": "https://api.together.xyz/v1", "protocol": "openai-chat", "api_key_env": "TOGETHER_API_KEY"}
			})
			redo, raw2, _, _, err := rv16Undo(t, s, g, inverse, undoBefore, raw, undoInForce)
			var conflict *config.ErrConflict
			if !errors.As(err, &conflict) {
				writeProjectFile(t, s.Project, raw2)
				got, _ := config.Load(s.Project, "")
				t.Fatalf("undo of undo: %v, want a conflict (reversal %+v); fallback now %s at %s", err, redo.Edits, got.Model.Fallback.Model, got.Model.Fallback.BaseURL)
			}
		})
	}
}
