package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// rv9zUndo runs the undo of a change that replaced bundle "mine" whose
// former bytes were before, and returns the bytes the undo would write.
func rv9zUndo(t *testing.T, s *Service, before []byte) []byte {
	t.Helper()
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	change := ResourceChange{Kind: "bundle", Name: "mine", Before: before, BeforeExists: true}
	inverse, err := change.Inverse()
	if err != nil {
		t.Fatal(err)
	}
	inverse.SchemaVersion, inverse.ExpectedVersion = SchemaVersion, view.Snapshot.Version
	editor, err := config.LockEditor(context.Background(), s.Project)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := s.PrepareRestore(editor, inverse, s.Grant(), before)
	editor.Close()
	if err != nil {
		t.Fatalf("undo refused: %v", err)
	}
	return restore.After
}

// A bundle main saved sits beside a project file that sets what main's
// bundle does not name (a setting main did not have, the phone switch, the
// model's effort). The bundle loads with the project file's values. The
// user deletes it (or saves over it) and undoes that: the undo writes the
// bundle back decoded onto the built-in defaults and spelled out, so it
// now loads with the defaults' values instead.
func TestRV9ZUndoOfMainBundleKeepsWhatItLoadedWith(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	rv9WriteFile(t, config.File(project), []byte(`{"report_max_chars": 5000, "finalechat": {"enabled": false}, "model": {"reasoning_effort": "low"}}`))
	s := &Service{Project: project}
	// r9MainBundle as main writes it for an orchestrator effort of "" (send
	// none): main's BundleBytes omits an empty reasoning_effort.
	legacy := []byte(strings.Replace(r9MainBundle, "\"reasoning_effort\": \"medium\",\n    \"max_tokens\": 32768,\n    \"context_tokens\": 200000,\n    \"fallback\": null\n  },\n  \"task\"", "\"max_tokens\": 32768,\n    \"context_tokens\": 200000,\n    \"fallback\": null\n  },\n  \"task\"", 1))
	if bytes.Equal(legacy, []byte(r9MainBundle)) {
		t.Fatal("fixture: orchestrator effort not removed")
	}
	was := rv9Loaded(t, project, "mine", legacy)
	if was.ReportMaxChars != 5000 || was.Finalechat.Enabled == nil || *was.Finalechat.Enabled || was.Model.ReasoningEffort != "low" {
		t.Fatalf("setup: loaded report_max_chars %d, phone %v, effort %q", was.ReportMaxChars, was.Finalechat.Enabled, was.Model.ReasoningEffort)
	}
	// At 12aec43 this undo was refused (unknown field "orchestrator").
	old := config.Defaults()
	dec := json.NewDecoder(bytes.NewReader(legacy))
	dec.DisallowUnknownFields()
	t.Logf("12aec43 reading of the bytes: %v", dec.Decode(&old))

	after := rv9zUndo(t, s, legacy)
	now := rv9Loaded(t, project, "mine", after)
	phone := "unset (on)"
	if now.Finalechat.Enabled != nil {
		phone = map[bool]string{true: "on", false: "off"}[*now.Finalechat.Enabled]
	}
	t.Logf("before the undo: report_max_chars 5000, phone off, effort low; after it: report_max_chars %d, phone %s, effort %q", now.ReportMaxChars, phone, now.Model.ReasoningEffort)
	if now.ReportMaxChars != was.ReportMaxChars || now.Finalechat.Enabled == nil || *now.Finalechat.Enabled != *was.Finalechat.Enabled || now.Model.ReasoningEffort != was.Model.ReasoningEffort {
		t.Fatalf("undo did not put back what was there: report_max_chars %d -> %d, phone off -> %s, effort %q -> %q", was.ReportMaxChars, now.ReportMaxChars, phone, was.Model.ReasoningEffort, now.Model.ReasoningEffort)
	}
}

// The same bundle, the plain r9MainBundle, saved from a remote settings
// save, against the same project file: BundleConfig's reading differs from
