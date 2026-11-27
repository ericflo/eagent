package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// The undo's three rules, one row each, and review 15's finding 12. A
// command is applied remotely, the file may be edited locally, and the
// command is undone as a connector undoes it:
//  1. the reversal is the keys the command changed, each put back to its
//     value before (unset where there was none), and no other key;
//  2. it is a conflict when a key of the reversal now differs from the file
//     the command wrote, and nothing else is;
//  3. the file it would write passes the checks a person's save of it does.
func TestK1ReversalRules(t *testing.T) {
	model := []control.Edit{{Op: "set", Key: "/model/model", Value: "gpt-6.1-sol"}}
	for _, row := range []struct {
		name    string
		env     map[string]string
		classes []string // the grant's classes; nil for all of them
		before  string
		edits   []control.Edit
		local   func(map[string]any) // a local edit after the command
		record  bool                 // the journal records the pairs in force at the command
		want    string               // ok, conflict, refused, or none (no undo)
		check   func(*testing.T, config.Config)
	}{
		// Finding 12, review 15: a model picked at the same provider writes
		// only the model, so its undo names only the model.
		{name: "f12.1 effort locked by EAGENT_REASONING_EFFORT", env: map[string]string{"EAGENT_REASONING_EFFORT": "low"}, before: `{"preset":"openai-high"}`, edits: model, want: "ok"},
		{name: "f12.2 preference-only grant", classes: []string{"preference"}, before: `{"preset":"openai-high"}`, edits: model, want: "ok"},
		{name: "f12.3 local edit of an untouched effort", before: `{"preset":"openai-high"}`, edits: model, local: func(m map[string]any) {
			m["model"].(map[string]any)["reasoning_effort"] = "low"
		}, want: "ok", check: func(t *testing.T, c config.Config) {
			if c.Model.ReasoningEffort != "low" || c.Model.Model == "gpt-6.1-sol" {
				t.Fatalf("after undo %s effort %q, want the preset's model at the local low", c.Model.Model, c.Model.ReasoningEffort)
			}
		}},
		// Rule 1.
		{name: "1: a pick at another provider is put back whole", before: `{"preset":"openai-high"}`, edits: []control.Edit{{Op: "set", Key: "/model/model", Value: "zai-org/GLM-5.3"}, {Op: "set", Key: "/model/base_url", Value: "https://api.together.xyz/v1"}}, want: "ok"},
		{name: "1: a preset change's pick is put back whole", before: `{"preset":"openai-high","model":{"reasoning_effort":"xhigh","context_tokens":400000}}`, edits: []control.Edit{{Op: "set", Key: "/preset", Value: "glm"}}, want: "ok"},
		{name: "1: a command that changed nothing has no undo", before: `{"tick_seconds":45}`, edits: []control.Edit{{Op: "set", Key: "/tick_seconds", Value: float64(45)}}, want: "none"},
		{name: "1: a null fallback written into is null again", before: `{"preset":"openai-high","model":{"fallback":null}}`, edits: []control.Edit{{Op: "set", Key: "/model/fallback/model", Value: "openai/gpt-6.1-sol"}}, want: "ok"},
		// Rule 2.
		{name: "2: a key of the reversal changed since", before: `{"tick_seconds":45}`, edits: []control.Edit{{Op: "set", Key: "/tick_seconds", Value: float64(30)}}, local: func(m map[string]any) {
			m["tick_seconds"] = float64(70)
		}, want: "conflict"},
		{name: "2: any other local edit is kept", before: `{"tick_seconds":45}`, edits: []control.Edit{{Op: "set", Key: "/tick_seconds", Value: float64(30)}}, local: func(m map[string]any) {
			m["persona"] = "concise"
		}, want: "ok", check: func(t *testing.T, c config.Config) {
			if c.TickSeconds != 45 || c.Persona != "concise" {
				t.Fatalf("after undo tick %d persona %q, want 45 and concise", c.TickSeconds, c.Persona)
			}
		}},
		{name: "2: a fallback put back to null conflicts on a local edit of it", before: `{"preset":"openai-high","model":{"fallback":null}}`, edits: []control.Edit{{Op: "set", Key: "/model/fallback/model", Value: "openai/gpt-6.1-sol"}}, local: func(m map[string]any) {
			m["model"].(map[string]any)["fallback"].(map[string]any)["max_tokens"] = float64(2000)
		}, want: "conflict"},
		{name: "2: a model set back conflicts on a local move of its address", before: `{"preset":"openai-high","model":{"model":"gpt-6.1-sol"}}`, edits: []control.Edit{{Op: "set", Key: "/model/model", Value: "gpt-6-astra"}}, local: func(m map[string]any) {
			m["model"].(map[string]any)["base_url"] = "https://openrouter.ai/api/v1"
		}, want: "conflict"},
		{name: "2: a model taken out does not conflict on a local move of its address", before: `{"preset":"openai-high"}`, edits: model, local: func(m map[string]any) {
			m["model"].(map[string]any)["base_url"] = "https://api.openai.com/v1/"
		}, want: "ok", check: func(t *testing.T, c config.Config) {
			if c.Model.Model == "gpt-6.1-sol" || c.Model.BaseURL != "https://api.openai.com/v1/" {
				t.Fatalf("after undo %s @ %s, want the preset's model at the local address", c.Model.Model, c.Model.BaseURL)
			}
		}},
		// Rule 3: a key variable the command replaced, which nothing saved
		// today names, is not a choice a person has here now, unless the
		// journal recorded its route's pair as in force at the command (a
		// journal of an earlier eagent records none).
		{name: "3: a key variable no longer offered, pair recorded at the command", record: true, before: `{"preset":"openai-high","model":{"api_key_env":"OPENAI_API_KEY_WORK"}}`, edits: []control.Edit{{Op: "set", Key: "/model/api_key_env", Value: "OPENAI_API_KEY"}}, want: "ok"},
		{name: "3: a key variable no longer offered, no record (an earlier journal)", before: `{"preset":"openai-high","model":{"api_key_env":"OPENAI_API_KEY_WORK"}}`, edits: []control.Edit{{Op: "set", Key: "/model/api_key_env", Value: "OPENAI_API_KEY"}}, want: "refused"},
	} {
		for _, fc := range []string{"", "off"} {
			t.Run(row.name+"/finalechat="+fc, func(t *testing.T) {
				clearRouteEnv(t)
				t.Setenv("EAGENT_FINALECHAT", fc)
				for k, v := range row.env {
					t.Setenv(k, v)
				}
				project := t.TempDir()
				writeProjectFile(t, project, row.before)
				want, err := config.Load(project, "")
				if err != nil {
					t.Fatal(err)
				}
				s := &Service{Project: project}
				g := s.Grant()
				if row.classes != nil {
					g.Classes = row.classes
				}
				view, err := s.RemoteSnapshot(g)
				if err != nil {
					t.Fatal(err)
				}
				cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: row.edits}
				var inForce [][2]string
				if row.record {
					inForce = RoutePairs(config.Resolve(project, s.Preset, s.Bundle).Effective)
				}
				if _, _, err := s.Apply(context.Background(), cmd, g); err != nil {
					t.Fatalf("apply: %v", err)
				}
				desired, _ := os.ReadFile(config.File(project))
				if row.local != nil {
					var m map[string]any
					_ = json.Unmarshal(desired, &m)
					row.local(m)
					edited, _ := json.Marshal(m)
					writeProjectFile(t, project, string(edited))
				}
				inverse, err := ConfigInverse(cmd, row.before, string(desired))
				if row.want == "none" {
					if err == nil {
						t.Fatalf("undo offered for a command that changed nothing: %+v", inverse.Edits)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				b, _ := fileValues(row.before)
				d, _ := fileValues(string(desired))
				for _, e := range inverse.Edits {
					bv, hb := pointerValue(b, e.Key)
					dv, hd := pointerValue(d, e.Key)
					if hb == hd && reflect.DeepEqual(bv, dv) {
						t.Fatalf("the reversal names %s, which the command left as it was\nreversal %+v\ncommand wrote:\n%s", e.Key, inverse.Edits, desired)
					}
				}
				now, err := s.RemoteSnapshot(g)
				if err != nil {
					t.Fatal(err)
				}
				inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, now.Snapshot.Version
				raw, _, _, err := s.PrepareUndo(inverse, row.before, string(desired), g, inForce...)
				var conflict *config.ErrConflict
				switch {
				case row.want == "conflict" && !errors.As(err, &conflict):
					t.Fatalf("undo: %v, want a conflict (reversal %+v)", err, inverse.Edits)
				case row.want == "refused" && (err == nil || errors.As(err, &conflict) || !strings.Contains(err.Error(), "nothing was changed")):
					t.Fatalf("undo: %v, want it refused saying nothing was changed (reversal %+v)\nwrote:\n%s", err, inverse.Edits, raw)
				case row.want == "ok" && err != nil:
					t.Fatalf("undo: %v (reversal %+v)\ncommand wrote:\n%s", err, inverse.Edits, desired)
				}
				if row.want != "ok" {
					return
				}
				writeProjectFile(t, project, raw)
				got, err := config.Load(project, "")
				if err != nil {
					t.Fatalf("load after undo: %v\n%s", err, raw)
				}
				if row.check != nil {
					row.check(t, got)
					return
				}
				if !reflect.DeepEqual(got.Model, want.Model) || got.Preset != want.Preset || !reflect.DeepEqual(got.Summarizer, want.Summarizer) {
					t.Fatalf("after undo %s %+v, want %s %+v\nundo wrote:\n%s", got.Preset, got.Model, want.Preset, want.Model, raw)
				}
			})
		}
	}
}
