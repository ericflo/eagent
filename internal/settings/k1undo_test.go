package settings

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// rv15UndoAfterLocal applies edits remotely to before, then makes a local
// edit to the file the command wrote, then undoes the command. It returns
// the file the undo writes (or the undo's error) and the inverse reviewed.
func rv15UndoAfterLocal(t *testing.T, project, before string, edits []control.Edit, local func(map[string]any)) (string, []control.Edit, string, error) {
	t.Helper()
	writeProjectFile(t, project, before)
	s := &Service{Project: project}
	view := apply(t, s, edits...)
	desired, _ := os.ReadFile(config.File(project))
	var m map[string]any
	if err := json.Unmarshal(desired, &m); err != nil {
		t.Fatal(err)
	}
	local(m)
	edited, _ := json.Marshal(m)
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
	raw, _, _, err := s.PrepareUndo(inverse, before, string(desired), s.Grant())
	return raw, inverse.Edits, string(desired), err
}

// A model the user sets locally after a remote /preset change is a local
// edit of a field the command did not write: the undo either keeps it or
// is refused, and never takes it out without saying so in its review.
func TestRV15UndoOfPresetChangeKeepsALocalModel(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	before := `{"preset":"openai-high"}`
	edits := []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-med"}}
	raw, inv, desired, err := rv15UndoAfterLocal(t, project, before, edits, func(m map[string]any) {
		m["model"] = map[string]any{"model": "claude-fable-5-1"}
	})
	if err != nil {
		t.Logf("undo refused: %v (acceptable)", err)
		return
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(raw), &got)
	if v, _ := pointerValue(got, "/model/model"); v != "claude-fable-5-1" {
		t.Fatalf("undo went through and the local model is %v, want claude-fable-5-1 kept\nreviewed inverse: %+v\ncommand wrote:\n%s\nundo wrote:\n%s", v, inv, desired, raw)
	}
}

// A window the user sets locally after a remote /preset change is likewise
// a local edit the command did not write.
func TestRV15UndoOfPresetChangeKeepsALocalWindow(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	before := `{"preset":"openai-high","model":{"max_tokens":4000}}`
	edits := []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-med"}}
	raw, inv, desired, err := rv15UndoAfterLocal(t, project, before, edits, func(m map[string]any) {
		route, _ := m["model"].(map[string]any)
		route["context_tokens"] = float64(300000)
	})
	if err != nil {
		t.Logf("undo refused: %v (acceptable)", err)
		return
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(raw), &got)
	if v, _ := pointerValue(got, "/model/context_tokens"); v != float64(300000) {
		t.Fatalf("undo went through and the local window is %v, want 300000 kept\nreviewed inverse: %+v\ncommand wrote:\n%s\nundo wrote:\n%s", v, inv, desired, raw)
	}
}

// An effort the user sets locally after a remote /preset change, which the
// model of the preset the undo restores does not offer (GLM-5.3 offers low,
// medium and high), is a local edit the command did not write: the undo
// keeps it as set, or is refused.
func TestK1UndoOfPresetChangeKeepsALocalEffortTheOldModelLacks(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	before := `{"preset":"glm"}`
	edits := []control.Edit{{Op: "set", Key: "/preset", Value: "openai-high"}}
	raw, inv, desired, err := rv15UndoAfterLocal(t, project, before, edits, func(m map[string]any) {
		m["model"] = map[string]any{"reasoning_effort": "xhigh"}
	})
	if err != nil {
		t.Logf("undo refused: %v (acceptable)", err)
		return
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(raw), &got)
	if v, _ := pointerValue(got, "/model/reasoning_effort"); v != "xhigh" {
		t.Fatalf("undo went through and the local effort is %v, want xhigh kept\nreviewed inverse: %+v\ncommand wrote:\n%s\nundo wrote:\n%s", v, inv, desired, raw)
	}
}

// The undo of a remote edit that took a dedicated summarizer's route out
// writes back the route's fields and no other: the main route's, which a
// person's edit of a summarizer with no route starts from, are not seeded
// into the reversal (the main route's max_tokens was).
func TestK1UndoWritesOnlyItsReversalIntoASummarizerRoute(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	before := `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3-Flash","api_key_env":"TOGETHER_API_KEY"}}}`
	var edits []control.Edit
	for _, leaf := range []string{"protocol", "base_url", "model", "api_key_env"} {
		edits = append(edits, control.Edit{Op: "unset", Key: "/summarizer/model/" + leaf})
	}
	writeProjectFile(t, project, before)
	want, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	raw := r11Undo(t, project, before, edits)
	var gotFile, wantFile map[string]any
	_ = json.Unmarshal([]byte(raw), &gotFile)
	_ = json.Unmarshal([]byte(before), &wantFile)
	writeProjectFile(t, project, raw)
	got, err := config.Load(project, "")
	if err != nil {
		t.Fatalf("load: %v\n%s", err, raw)
	}
	g, _ := json.Marshal(got.Summarizer)
	w, _ := json.Marshal(want.Summarizer)
	if string(g) != string(w) || !reflect.DeepEqual(gotFile, wantFile) {
		t.Fatalf("summarizer after undo %s\nwant %s\nundo wrote:\n%s", g, w, raw)
	}
}

// A fallback turned off with null, edited remotely, then a local edit of an
// unrelated field: the undo goes through, keeps the edit, and the fallback
// is off again.
func TestRV15UndoOfNullFallbackEditKeepsUnrelatedLocal(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			project := t.TempDir()
			before := `{"preset":"openai-high","model":{"fallback":null}}`
			edits := []control.Edit{{Op: "set", Key: "/model/fallback/model", Value: "openai/gpt-6.1-sol"}}
			raw, inv, desired, err := rv15UndoAfterLocal(t, project, before, edits, func(m map[string]any) {
				route, _ := m["model"].(map[string]any)
				route["max_tokens"] = float64(5000)
			})
			if err != nil {
				t.Fatalf("undo refused: %v (inverse %+v)\n%s", err, inv, desired)
			}
			writeProjectFile(t, project, raw)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("load: %v\n%s", err, raw)
			}
			if got.Model.Fallback != nil || got.Model.MaxTokens != 5000 {
				t.Fatalf("after undo fallback %+v max %d\n%s", got.Model.Fallback, got.Model.MaxTokens, raw)
			}
		})
	}
}

// The round-14 reader's case, once more, both ways of EAGENT_FINALECHAT.
func TestRV15Round14ReaderCase(t *testing.T) {
	for _, fc := range []string{"", "off"} {
		t.Run("finalechat="+fc, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", fc)
			project := t.TempDir()
			before := `{"preset":"openai-high","model":{"max_tokens":4000}}`
			edits := []control.Edit{{Op: "set", Key: "/preset", Value: "anthropic-med"}}
			raw, inv, desired, err := rv15UndoAfterLocal(t, project, before, edits, func(m map[string]any) {
				route, _ := m["model"].(map[string]any)
				route["reasoning_effort"] = "high"
			})
			if err != nil {
				t.Fatalf("undo refused: %v (inverse %+v)\n%s", err, inv, desired)
			}
			writeProjectFile(t, project, raw)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("load: %v\n%s", err, raw)
			}
			if got.Preset != "openai-high" || got.Model.ReasoningEffort != "high" || got.Model.MaxTokens != 4000 {
				t.Fatalf("after undo %s effort %q max %d\n%s", got.Preset, got.Model.ReasoningEffort, got.Model.MaxTokens, raw)
			}
		})
	}
}
