package settings

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// rv16Undo runs one connector-style undo of a journal (before, desired,
// inForce) and returns the file it would write and the undo's own journal.
func rv16Undo(t *testing.T, s *Service, g control.Grant, applied control.Proposal, before, desired string, inForce [][2]string) (inverse control.Proposal, raw, undoBefore string, undoInForce [][2]string, err error) {
	t.Helper()
	inverse, err = ConfigInverse(applied, before, desired)
	if err != nil {
		return
	}
	now, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, now.Snapshot.Version
	res := config.Resolve(s.Project, s.Preset, s.Bundle)
	undoBefore = res.File.Raw
	if undoBefore == "" {
		undoBefore = "{}\n"
	}
	undoInForce = RoutePairs(res.Effective)
	raw, _, _, err = s.PrepareUndo(inverse, before, desired, g, inForce...)
	return
}

// Only an undo's reversal may set a route to null: a person's proposal of
// the same edit names no field and is refused.
func TestK2APersonCannotSetARouteToNull(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, `{"preset":"openai-high"}`)
	if err := tryApply(t, &Service{Project: project}, control.Edit{Op: "set", Key: "/model/fallback"}); err == nil {
		t.Fatal("a person's proposal set the fallback to null")
	}
}

// An undo of an undo returns to the command's result.
func TestRV16UndoOfUndoReturns(t *testing.T) {
	for _, row := range []struct {
		name   string
		before string
		edits  []control.Edit
	}{
		{"null fallback written into", `{"preset":"openai-high","model":{"fallback":null}}`, []control.Edit{{Op: "set", Key: "/model/fallback/model", Value: "openai/gpt-6.1-sol"}}},
		{"preset pick with a work key", `{"preset":"openai-high","model":{"api_key_env":"OPENAI_API_KEY_WORK"}}`, []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-high"}}},
		{"pick at another provider", `{"preset":"openai-high"}`, []control.Edit{{Op: "set", Key: "/model/model", Value: "zai-org/GLM-5.3"}, {Op: "set", Key: "/model/base_url", Value: "https://api.together.xyz/v1"}}},
		{"summarizer seeded", `{"preset":"openai-high","summarizer":{"mode":"dedicated"}}`, []control.Edit{{Op: "set", Key: "/summarizer/model/model", Value: "gpt-6.1-sol"}}},
		{"summarizer null route", `{"preset":"openai-high","summarizer":{"mode":"dedicated","model":null}}`, []control.Edit{{Op: "set", Key: "/summarizer/model/model", Value: "gpt-6.1-sol"}}},
		{"null effort", `{"preset":"openai-high","model":{"reasoning_effort":null}}`, []control.Edit{{Op: "set", Key: "/model/reasoning_effort", Value: "low"}}},
	} {
		for _, fc := range []string{"", "off"} {
			t.Run(row.name+"/finalechat="+fc, func(t *testing.T) {
				clearRouteEnv(t)
				t.Setenv("EAGENT_FINALECHAT", fc)
				project := t.TempDir()
				writeProjectFile(t, project, row.before)
				s := &Service{Project: project}
				g := s.Grant()
				view, err := s.RemoteSnapshot(g)
				if err != nil {
					t.Fatal(err)
				}
				inForce := RoutePairs(config.Resolve(project, s.Preset, s.Bundle).Effective)
				cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: row.edits}
				if _, _, err := s.Apply(context.Background(), cmd, g); err != nil {
					t.Fatalf("apply: %v", err)
				}
				desiredB, _ := os.ReadFile(config.File(project))
				desired := string(desiredB)
				result, err := config.Load(project, "")
				if err != nil {
					t.Fatal(err)
				}
				inverse, raw, undoBefore, undoInForce, err := rv16Undo(t, s, g, cmd, row.before, desired, inForce)
				if err != nil {
					t.Fatalf("undo: %v", err)
				}
				writeProjectFile(t, project, raw)
				_, raw2, _, _, err := rv16Undo(t, s, g, inverse, undoBefore, raw, undoInForce)
				if err != nil {
					t.Fatalf("undo of undo: %v\nundo wrote:\n%s", err, raw)
				}
				writeProjectFile(t, project, raw2)
				got, err := config.Load(project, "")
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.Model, result.Model) || got.Preset != result.Preset || !reflect.DeepEqual(got.Summarizer, result.Summarizer) {
					t.Fatalf("after undo of undo %s %+v %+v, want the command's %s %+v %+v\ncommand wrote:\n%s\nundo of undo wrote:\n%s", got.Preset, got.Model, got.Summarizer, result.Preset, result.Model, result.Summarizer, desired, raw2)
				}
			})
		}
	}
}

// The review an undo offers (its edits) is what the undo writes: applying
// the reviewed edits as an ordinary command gives the same configuration
// as the undo.
func TestRV16ReviewIsWhatUndoWrites(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			before := `{"preset":"openai-high","model":{"fallback":null}}`
			project := t.TempDir()
			writeProjectFile(t, project, before)
			s := &Service{Project: project}
			g := s.Grant()
			view, err := s.RemoteSnapshot(g)
			if err != nil {
				t.Fatal(err)
			}
			cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: []control.Edit{{Op: "set", Key: "/model/fallback/model", Value: "openai/gpt-6.1-sol"}}}
			if _, _, err := s.Apply(context.Background(), cmd, g); err != nil {
				t.Fatalf("apply: %v", err)
			}
			desiredB, _ := os.ReadFile(config.File(project))
			inverse, raw, _, _, err := rv16Undo(t, s, g, cmd, before, string(desiredB), nil)
			if err != nil {
				t.Fatalf("undo: %v", err)
			}
			reviewed, _ := json.Marshal(inverse.Edits)
			// The reviewed edits, written as a command of their own.
			other := t.TempDir()
			writeProjectFile(t, other, string(desiredB))
			o := &Service{Project: other}
			og := o.Grant()
			ov, err := o.RemoteSnapshot(og)
			if err != nil {
				t.Fatal(err)
			}
			asReviewed := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: ov.Snapshot.Version, Edits: inverse.Edits}
			reviewedRaw, _, _, err := o.prepare(asReviewed, og, true, nil)
			if err != nil {
				t.Fatalf("reviewed edits: %v", err)
			}
			writeProjectFile(t, project, raw)
			undone, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			writeProjectFile(t, other, reviewedRaw)
			review, err := config.Load(other, "")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(undone.Model.Fallback, review.Model.Fallback) {
				t.Fatalf("the review shown is %s; with those edits the fallback is %+v, but the undo writes a fallback of %+v\nundo wrote:\n%s", reviewed, review.Model.Fallback, undone.Model.Fallback, raw)
			}
		})
	}
}
