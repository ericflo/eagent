//go:build !rv16old

package settings

import (
	"context"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// A dedicated summarizer with no route of its own runs on the main route. A
// command gives it a model at another provider; the person then moves the
// main route by hand; the undo takes the summarizer's route out again, so it
// follows the main route as it did before the command (now the person's).
// That is no conflict: the undo sends no model anywhere the person's own
// choice does not.
func TestK4UndoReturnsASummarizerToFollowingTheMainRoute(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			before := `{"preset":"openai-high","summarizer":{"mode":"dedicated"}}`
			s, g, cmd, desired, inForce := rv17Setup(t, before, []control.Edit{
				{Op: "set", Key: "/summarizer/model/model", Value: "zai-org/GLM-5.3-Flash"},
				{Op: "set", Key: "/summarizer/model/base_url", Value: "https://api.together.xyz/v1"},
			})
			rv17Local(t, s, func(m map[string]any) { m["preset"] = "anthropic-med" })
			inverse, raw, _, _, err := rv16Undo(t, s, g, cmd, before, desired, inForce)
			if err != nil {
				t.Fatalf("undo: %v (reversal %+v)", err, inverse.Edits)
			}
			writeProjectFile(t, s.Project, raw)
			got, err := config.Load(s.Project, "")
			if err != nil {
				t.Fatal(err)
			}
			if r := got.Summarizer.Route(got.Model); r.Model != got.Model.Model || r.BaseURL != got.Model.BaseURL {
				t.Fatalf("after undo the summarizer runs %s at %s, not the main route's %s at %s\n%s", r.Model, r.BaseURL, got.Model.Model, got.Model.BaseURL, raw)
			}
		})
	}
}

// A main route the file had as null, a command that wrote one field of it
// (an effort), and the model pinned by EAGENT_MODEL: the undo goes through,
// as at f36e19c, where it unset the effort.
func TestRV18NullRouteUndoUnderPinnedModel(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			t.Setenv("EAGENT_MODEL", "gpt-6.1-sol")
			before := `{"preset":"openai-high","model":null}`
			project := t.TempDir()
			writeProjectFile(t, project, before)
			s := &Service{Project: project}
			g := s.Grant()
			view, err := s.RemoteSnapshot(g)
			if err != nil {
				t.Fatal(err)
			}
			inForce := RoutePairs(config.Resolve(project, s.Preset, s.Bundle).Effective)
			cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/model/reasoning_effort", Value: "low"}}}
			if _, _, err := s.Apply(context.Background(), cmd, g); err != nil {
				t.Fatalf("apply: %v", err)
			}
			desired, _ := os.ReadFile(config.File(project))
			inverse, raw, _, _, err := rv16Undo(t, s, g, cmd, before, string(desired), inForce)
			if err != nil {
				t.Fatalf("undo: %v (reversal %+v)\ncommand wrote:\n%s", err, inverse.Edits, desired)
			}
			writeProjectFile(t, project, raw)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Model.ReasoningEffort == "low" {
				t.Fatalf("after undo the effort is still low:\n%s", raw)
			}
		})
	}
}
