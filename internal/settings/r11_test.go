package settings

import (
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
	inverse, err := ConfigInverse(control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: edits}, before)
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
