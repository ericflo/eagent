package settings

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// E1 (round 14 finding 20): a remote /preset set or unset alone is a pick of
// the model route: the effort kept moves to one the new preset's model
// offers, and a model the file names for the old preset's address, without
// the address, is not sent to the new one's. Undo puts the file back as it
// was.
func TestK1RemotePresetAloneIsAPick(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		edit       control.Edit
	}{
		{"effort, set glm", `{"preset":"openai-high","model":{"reasoning_effort":"xhigh"}}`, control.Edit{Op: "set", Key: "/preset", Value: "glm"}},
		{"effort, unset", `{"preset":"openai-high","model":{"reasoning_effort":"xhigh"}}`, control.Edit{Op: "unset", Key: "/preset"}},
		{"model, set glm", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol"}}`, control.Edit{Op: "set", Key: "/preset", Value: "glm"}},
		{"model, unset", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol"}}`, control.Edit{Op: "unset", Key: "/preset"}},
		{"model and effort, unset", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol","reasoning_effort":"xhigh"}}`, control.Edit{Op: "unset", Key: "/preset"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", "")
			project := t.TempDir()
			writeProjectFile(t, project, tc.file)
			before, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := tryApply(t, &Service{Project: project}, tc.edit); err != nil {
				t.Fatal(err)
			}
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			written, _ := os.ReadFile(config.File(project))
			// The defaults' route (glm's), at the effort the clamp a
			// session's preset pick uses gives what the file kept.
			want := config.Defaults()
			var file struct {
				Model config.ModelRoute `json:"model"`
			}
			_ = json.Unmarshal([]byte(tc.file), &file)
			if file.Model.ReasoningEffort != "" {
				want.Model.ReasoningEffort = file.Model.ReasoningEffort
				config.OfferedEffort(project, &want)
			}
			if got.Model.Model != want.Model.Model || got.Model.BaseURL != want.Model.BaseURL || got.Model.ReasoningEffort != want.Model.ReasoningEffort {
				t.Fatalf("got %s @ %s effort %q, want %s @ %s effort %q\n%s", got.Model.Model, got.Model.BaseURL, got.Model.ReasoningEffort, want.Model.Model, want.Model.BaseURL, want.Model.ReasoningEffort, written)
			}
			raw := r11Undo(t, project, tc.file, []control.Edit{tc.edit})
			writeProjectFile(t, project, raw)
			undone, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("load after undo: %v\n%s", err, raw)
			}
			if !reflect.DeepEqual(undone.Model, before.Model) || undone.Preset != before.Preset {
				t.Fatalf("after undo %s %+v, want %s %+v\nundo wrote:\n%s", undone.Preset, undone.Model, before.Preset, before.Model, raw)
			}
		})
	}
}

// E1: a journal stored before the pick rule holds the file the preset
// change wrote then, with the route as it was. Its undo is the one the user
// reviewed (the inverse, and so its digest, is unchanged) and puts back the
// file as it was before the change.
func TestK1UndoOfAPresetChangeJournaledBeforeThePick(t *testing.T) {
	for _, tc := range []struct {
		name, before, desired string
		edit                  control.Edit
	}{
		{"effort, set glm", `{"preset":"openai-high","model":{"reasoning_effort":"xhigh"}}`, `{"preset":"glm","model":{"reasoning_effort":"xhigh"}}`, control.Edit{Op: "set", Key: "/preset", Value: "glm"}},
		{"model, set glm", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol"}}`, `{"preset":"glm","model":{"model":"gpt-6.1-sol"}}`, control.Edit{Op: "set", Key: "/preset", Value: "glm"}},
		{"model, unset", `{"preset":"openai-high","model":{"model":"gpt-6.1-sol"}}`, `{"model":{"model":"gpt-6.1-sol"}}`, control.Edit{Op: "unset", Key: "/preset"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", "")
			project := t.TempDir()
			writeProjectFile(t, project, tc.before)
			want, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			writeProjectFile(t, project, tc.desired)
			s := &Service{Project: project}
			view, err := s.RemoteSnapshot(s.Grant())
			if err != nil {
				t.Fatal(err)
			}
			inverse, err := ConfigInverse(control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: "earlier", Edits: []control.Edit{tc.edit}}, tc.before, tc.desired)
			if err != nil {
				t.Fatal(err)
			}
			if old := []control.Edit{{Op: "set", Key: "/preset", Value: "openai-high"}}; !reflect.DeepEqual(inverse.Edits, old) {
				t.Fatalf("inverse %+v, want the one reviewed then, %+v", inverse.Edits, old)
			}
			inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, view.Snapshot.Version
			raw, _, _, err := s.PrepareUndo(inverse, tc.desired, s.Grant())
			if err != nil {
				t.Fatalf("undo: %v", err)
			}
			writeProjectFile(t, project, raw)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("load after undo: %v\n%s", err, raw)
			}
			if !reflect.DeepEqual(got.Model, want.Model) || got.Preset != want.Preset {
				t.Fatalf("after undo %s %+v, want %s %+v\nundo wrote:\n%s", got.Preset, got.Model, want.Preset, want.Model, raw)
			}
		})
	}
}

// E4: the undo of a pick of a fallback's own fallback puts back the fields
// the pick gave that route. Only a route no load checks can hold one (an
// inline summarizer's): on the main route and a dedicated summarizer's, a
// second fallback is refused as too deep.
func TestK1E4UndoWalksAFallbacksOwnFallback(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	before := `{"summarizer":{"mode":"inline","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3-Flash","api_key_env":"TOGETHER_API_KEY","fallback":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}}}`
	edits := []control.Edit{
		{Op: "set", Key: "/summarizer/model/fallback/fallback/base_url", Value: "https://api.anthropic.com/v1"},
		{Op: "set", Key: "/summarizer/model/fallback/fallback/model", Value: "claude-opus-5-5"},
	}
	raw := r11Undo(t, project, before, edits)
	var want, got map[string]any
	_ = json.Unmarshal([]byte(before), &want)
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("undo wrote\n%s\nwant\n%s", raw, before)
	}
}

// E1, the reader's case against 0b1c7df's undo: the undo of a preset change
// that wrote no field of the route goes through after a local edit of the
// route's effort, and keeps that edit.
func TestK1UndoOfAPresetChangeKeepsALocalEffort(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	before := `{"preset":"openai-high","model":{"max_tokens":4000}}`
	edits := []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-med"}}
	writeProjectFile(t, project, before)
	s := &Service{Project: project}
	view := apply(t, s, edits...)
	desired, _ := os.ReadFile(config.File(project))
	var local map[string]any
	if err := json.Unmarshal(desired, &local); err != nil {
		t.Fatal(err)
	}
	route, _ := local["model"].(map[string]any)
	if route == nil {
		t.Fatalf("after apply:\n%s", desired)
	}
	route["reasoning_effort"] = "high"
	edited, _ := json.Marshal(local)
	writeProjectFile(t, project, string(edited))
	after, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	inverse, err := ConfigInverse(control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}, before, string(desired))
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, after.Snapshot.Version
	raw, _, _, err := s.PrepareUndo(inverse, string(desired), s.Grant())
	if err != nil {
		t.Fatalf("undo refused: %v (inverse %+v)\nafter apply:\n%s", err, inverse.Edits, desired)
	}
	writeProjectFile(t, project, raw)
	got, err := config.Load(project, "")
	if err != nil {
		t.Fatalf("load after undo: %v\n%s", err, raw)
	}
	if got.Preset != "openai-high" || got.Model.ReasoningEffort != "high" || got.Model.MaxTokens != 4000 {
		t.Fatalf("after undo preset %q effort %q max_tokens %d, want openai-high, high, 4000\n%s", got.Preset, got.Model.ReasoningEffort, got.Model.MaxTokens, raw)
	}
}
