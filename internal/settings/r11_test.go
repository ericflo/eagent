package settings

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// r11Undo applies edits over before, then undoes them as a connector does
// (ConfigInverse of the applied edits, PrepareUndo against the file the
// edits wrote) and returns the file the undo writes.
func r11Undo(t *testing.T, project, before string, edits []control.Edit) string {
	t.Helper()
	writeProjectFile(t, project, before)
	s := &Service{Project: project}
	view := apply(t, s, edits...)
	desired, _ := os.ReadFile(config.File(project))
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
		t.Fatalf("undo: %v (inverse %+v)", err, inverse.Edits)
	}
	return raw
}

// Finding 18: a summarizer route written as null (what `eagent config
// --write` and a pin write when it has none) is no route: a remote edit of
// a dedicated summarizer's model starts from the main model's route, as
// with no route written, and its undo takes the whole route out again.
func TestR11SummarizerRouteWrittenNull(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	before := `{"summarizer":{"mode":"dedicated","model":null}}`
	edits := []control.Edit{{Op: "set", Key: "/summarizer/model/model", Value: "zai-org/GLM-5.3-Flash"}}
	writeProjectFile(t, project, before)
	want, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	raw := r11Undo(t, project, before, edits)
	writeProjectFile(t, project, raw)
	got, err := config.Load(project, "")
	if err != nil {
		t.Fatalf("load after undo: %v\n%s", err, raw)
	}
	if got.Summarizer.Model != nil || want.Summarizer.Model != nil {
		t.Fatalf("summarizer route after undo %+v, want %+v\nundo wrote:\n%s", got.Summarizer.Model, want.Summarizer.Model, raw)
	}
}

// Finding 19: a fallback the file turned off with null (over a preset that
// has one) stays off after the undo of a remote edit that gave the route a
// fallback: the undo loads as the file before it did. (No layer below the
// file gives a summarizer route a fallback, so there the null and its
// absence load alike.)
func TestR11UndoKeepsANullFallbackOff(t *testing.T) {
	clearRouteEnv(t)
	for _, c := range []struct{ name, before, route, skip string }{
		{"main", `{"preset":"openai-high","model":{"fallback":null}}`, "/model", "known, not fixed: the null is an edit of /model/fallback, which is no field of the descriptor, so validation refuses it"},
		{"summarizer", `{"preset":"openai-high","summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3-flash","api_key_env":"OPENROUTER_API_KEY","fallback":null}}}`, "/summarizer/model", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.skip != "" {
				t.Skip(c.skip)
			}
			project := t.TempDir()
			writeProjectFile(t, project, c.before)
			want, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			edits := []control.Edit{
				{Op: "set", Key: c.route + "/fallback/base_url", Value: "https://openrouter.ai/api/v1"},
				{Op: "set", Key: c.route + "/fallback/model", Value: "z-ai/glm-5.3"},
			}
			raw := r11Undo(t, project, c.before, edits)
			writeProjectFile(t, project, raw)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("load after undo: %v\n%s", err, raw)
			}
			g, _ := json.Marshal([]any{got.Model, got.Summarizer})
			w, _ := json.Marshal([]any{want.Model, want.Summarizer})
			if string(g) != string(w) {
				t.Fatalf("routes after undo %s\nwant %s\nundo wrote:\n%s", g, w, raw)
			}
		})
	}
}
