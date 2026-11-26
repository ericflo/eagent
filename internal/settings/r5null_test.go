package settings

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// config.json must be an object: a raw save of JSON null (or another
// non-object) is refused and nothing is written.
func TestRawSaveRefusesAFileThatIsNotAnObject(t *testing.T) {
	clearRouteEnv(t)
	for _, raw := range []string{"null", "[]", "3"} {
		project := t.TempDir()
		if _, err := (&Service{Project: project}).SaveRaw(context.Background(), RawRequest{Raw: raw}); err == nil {
			t.Fatalf("%s was saved", raw)
		}
		if _, err := os.Stat(config.File(project)); !os.IsNotExist(err) {
			t.Fatalf("%s: a file was written (%v)", raw, err)
		}
		if _, err := config.SaveRaw(project, raw, "", false); err == nil {
			t.Fatalf("config.SaveRaw saved %s", raw)
		}
	}
}

// A null in config.json clears what the preset sets for that field. The
// phone is told the file sets it (to null), so it can offer to put the
// preset's value back.
func TestRemoteSnapshotShowsANullTheFileSaves(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeFile(t, config.File(project), `{"preset":"openai-high","model":{"reasoning_effort":null}}`)
	s := &Service{Project: project}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	v, ok := view.Snapshot.Saved["/model/reasoning_effort"]
	if !ok || v != nil {
		t.Fatalf("saved /model/reasoning_effort = %v, %v", v, ok)
	}
	if _, ok := view.Snapshot.Saved["/model/max_tokens"]; ok {
		t.Fatal("a field the file does not name is shown as saved")
	}
}

// A config.json that already holds null loads as no file at all; saving a
// change over it, from the page or the phone, writes the change instead of
// crashing the server.
func TestSavesOverANullFileWriteTheChange(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeFile(t, config.File(project), "null\n")
	res := config.Resolve(project, "", "")
	if res.LoadError != "" {
		t.Fatal(res.LoadError)
	}
	cfg := res.Effective
	cfg.Persona = "terse"
	raw, _ := json.Marshal(cfg)
	if _, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(project, "")
	if err != nil || loaded.Persona != "terse" {
		t.Fatalf("after the save: %q %v", loaded.Persona, err)
	}
	values, err := fileValues("null")
	if err != nil {
		t.Fatal(err)
	}
	setPointer(values, "/persona", "plain", false)
	if values["persona"] != "plain" {
		t.Fatalf("values = %v", values)
	}
}
