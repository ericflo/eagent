package settings

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// Finding 21: a field the file cleared with an explicit null (an effort or
// a reply ceiling under a preset that sets one) is cleared again by the
// undo of a remote edit of it, not unset to the preset's. (A window
// follows the model in use, by the owner's rule, and is unset as before.)
func TestR10UndoKeepsANullClearedField(t *testing.T) {
	clearRouteEnv(t)
	for _, c := range []struct {
		leaf  string
		value any
	}{
		{"reasoning_effort", "low"},
		{"max_tokens", float64(4096)},
	} {
		t.Run(c.leaf, func(t *testing.T) {
			project := t.TempDir()
			before := `{"preset":"glm","model":{"` + c.leaf + `":null}}`
			writeProjectFile(t, project, before)
			want, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			s := &Service{Project: project}
			edits := []control.Edit{{Op: "set", Key: "/model/" + c.leaf, Value: c.value}}
			view := apply(t, s, edits...)
			desired, _ := os.ReadFile(config.File(project))
			after, err := s.RemoteSnapshot(s.Grant())
			if err != nil {
				t.Fatal(err)
			}
			inverse, err := ConfigInverse(control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}, before)
			if err != nil {
				t.Fatal(err)
			}
			inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, after.Snapshot.Version
			raw, _, _, err := s.PrepareUndo(inverse, string(desired), s.Grant())
			if err != nil {
				t.Fatalf("undo: %v (inverse %+v)", err, inverse.Edits)
			}
			writeProjectFile(t, project, raw)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			g, _ := json.Marshal(got.Model)
			w, _ := json.Marshal(want.Model)
			if string(g) != string(w) {
				t.Fatalf("route after undo %s, want %s\nundo wrote:\n%s", g, w, raw)
			}
		})
	}
}
