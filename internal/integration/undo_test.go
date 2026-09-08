package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/prompts"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

func reviewedCommand(t *testing.T, s *settings.Service, g control.Grant, p control.Proposal) command {
	t.Helper()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	p.SchemaVersion, p.ExpectedVersion = settings.SchemaVersion, view.Snapshot.Version
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return command{ID: randomID(), UserID: "test-user", Proposal: p, Digest: artifact.Digest(raw), Expires: time.Now().Add(time.Minute)}
}

func reviewedUndo(t *testing.T, s *settings.Service, g control.Grant, result commandJournal) command {
	t.Helper()
	offer, ok := result.Result["undo"].(map[string]any)
	if !ok {
		t.Fatalf("no undo review for command: %#v", result.Result)
	}
	return reviewedCommand(t, s, g, control.Proposal{Operation: "settings.undo", Parameters: map[string]any{"command_id": offer["command_id"], "restore_sha256": offer["restore_sha256"]}})
}

func executeExpected(t *testing.T, s *settings.Service, g control.Grant, q command, want string) commandJournal {
	t.Helper()
	j, err := executeCommand(context.Background(), s, g, q, false)
	if err != nil || j.Status != want {
		t.Fatalf("command %s: want %s, got %s: %v %#v", q.Proposal.Operation, want, j.Status, err, j.Result)
	}
	return j
}

func TestUndoPreservesUnrelatedEditsAndCanItselfBeUndone(t *testing.T) {
	s, g, q := fixtureCommand(t)
	if _, err := config.SaveRaw(s.Project, `{"task_concurrency":3,"private_unknown":"do-not-export"}`, "", true); err != nil {
		t.Fatal(err)
	}
	q = reviewedCommand(t, s, g, q.Proposal)
	first := executeExpected(t, s, g, q, "succeeded")
	res := config.Resolve(s.Project, "", "")
	if _, err := config.SaveFile(s.Project, map[string]json.RawMessage{"persona": json.RawMessage(`"concise"`)}, res.File.ETag, true); err != nil {
		t.Fatal(err)
	}
	undo := reviewedUndo(t, s, g, first)
	second := executeExpected(t, s, g, undo, "succeeded")
	res = config.Resolve(s.Project, "", "")
	if res.Effective.TaskConcurrency != 3 || res.Effective.Persona != "concise" || !strings.Contains(res.File.Raw, "do-not-export") {
		t.Fatalf("undo lost original or unrelated settings: %s", res.File.Raw)
	}
	raw, _ := json.Marshal(second.Result)
	if strings.Contains(string(raw), "do-not-export") || second.Result["undoes"] != first.CommandID {
		t.Fatal("private journal leaked or undo correlation was lost")
	}
	before, _ := os.Stat(config.File(s.Project))
	duplicate, err := executeCommand(context.Background(), s, g, undo, true)
	after, _ := os.Stat(config.File(s.Project))
	if err != nil || duplicate.Status != "succeeded" || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("duplicate undo performed another write", err)
	}
	executeExpected(t, s, g, reviewedUndo(t, s, g, second), "succeeded")
	if got := config.Resolve(s.Project, "", "").Effective; got.TaskConcurrency != 6 || got.Persona != "concise" {
		t.Fatal("undo of undo failed to preserve unrelated values")
	}
}

func TestUndoConflictsOnAffectedFieldsAndRequiresExactReviewedTarget(t *testing.T) {
	s, g, q := fixtureCommand(t)
	first := executeExpected(t, s, g, q, "succeeded")
	bad := reviewedUndo(t, s, g, first)
	bad.Proposal.Parameters["restore_sha256"] = strings.Repeat("0", 64)
	executeExpected(t, s, g, bad, "rejected")
	res := config.Resolve(s.Project, "", "")
	if _, err := config.SaveFile(s.Project, map[string]json.RawMessage{"task_concurrency": json.RawMessage("7")}, res.File.ETag, true); err != nil {
		t.Fatal(err)
	}
	executeExpected(t, s, g, reviewedUndo(t, s, g, first), "conflicted")
	if config.Resolve(s.Project, "", "").Effective.TaskConcurrency != 7 {
		t.Fatal("undo overwrote an intervening change to its field")
	}
}

func TestUndoRechecksCurrentFieldPermissionsAndOverrides(t *testing.T) {
	s, g, _ := fixtureCommand(t)
	q := reviewedCommand(t, s, g, control.Proposal{Operation: "settings.apply", Edits: []control.Edit{{Op: "set", Key: "/allow_outside_project", Value: true}}})
	first := executeExpected(t, s, g, q, "succeeded")
	restricted := g
	restricted.Classes = []string{"preference"}
	executeExpected(t, s, restricted, reviewedUndo(t, s, restricted, first), "rejected")
	if !config.Resolve(s.Project, "", "").Effective.AllowOutsideProject {
		t.Fatal("undo bypassed the current permission grant")
	}
	q = reviewedCommand(t, s, g, control.Proposal{Operation: "settings.apply", Edits: []control.Edit{{Op: "set", Key: "/task_concurrency", Value: float64(6)}}})
	first = executeExpected(t, s, g, q, "succeeded")
	t.Setenv("EAGENT_TASK_CONCURRENCY", "9")
	executeExpected(t, s, g, reviewedUndo(t, s, g, first), "rejected")
}

func TestUndoPromptCreationAndRestorationUseConditionalResourceWriter(t *testing.T) {
	s, g, _ := fixtureCommand(t)
	name := prompts.Names[0]
	q := reviewedCommand(t, s, g, control.Proposal{Operation: "prompt.set", Parameters: map[string]any{"name": name, "text": "Previous prompt text"}})
	first := executeExpected(t, s, g, q, "succeeded")
	undo := reviewedUndo(t, s, g, first)
	second := executeExpected(t, s, g, undo, "succeeded")
	editor, err := config.LockEditor(context.Background(), s.Project)
	if err != nil {
		t.Fatal(err)
	}
	path, err := editor.RelatedPath("prompt", name)
	editor.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("undo did not remove the created override")
	}
	third := executeExpected(t, s, g, reviewedUndo(t, s, g, second), "succeeded")
	raw, _ := os.ReadFile(path)
	if string(raw) != "Previous prompt text" {
		t.Fatal("undo of prompt removal changed the original bytes")
	}
	if _, err := s.SavePrompt(context.Background(), name, "Locally changed prompt", false); err != nil {
		t.Fatal(err)
	}
	executeExpected(t, s, g, reviewedUndo(t, s, g, third), "conflicted")
}

func TestUndoBundleCreationCannotDeleteTheActiveDefault(t *testing.T) {
	s, g, _ := fixtureCommand(t)
	q := reviewedCommand(t, s, g, control.Proposal{Operation: "bundle.save", Parameters: map[string]any{"name": "undo-fixture"}})
	first := executeExpected(t, s, g, q, "succeeded")
	res := config.Resolve(s.Project, "", "")
	if _, err := config.SaveFile(s.Project, map[string]json.RawMessage{"default_config": json.RawMessage(`"undo-fixture"`)}, res.File.ETag, true); err != nil {
		t.Fatal(err)
	}
	executeExpected(t, s, g, reviewedUndo(t, s, g, first), "rejected")
	if _, err := os.Stat(config.BundlePath(s.Project, "undo-fixture")); err != nil {
		t.Fatal("undo deleted the active default bundle")
	}
}

func TestUndoReconcilesCompletedWriteAfterLostAcknowledgement(t *testing.T) {
	s, g, q := fixtureCommand(t)
	first := executeExpected(t, s, g, q, "succeeded")
	undo := reviewedUndo(t, s, g, first)
	completed := executeExpected(t, s, g, undo, "succeeded")
	completed.Status, completed.Result, completed.AuditRecorded = "prepared", nil, false
	if err := writeJSONAtomic(filepath.Join(stateDir(s.Project), "commands", undo.ID+".json"), completed); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(config.File(s.Project))
	recovered, err := executeCommand(context.Background(), s, g, undo, true)
	after, _ := os.Stat(config.File(s.Project))
	if err != nil || recovered.Status != "succeeded" || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("undo did not reconcile its completed write", err)
	}
}

func TestLargeUndoReviewCannotBlockAcknowledgement(t *testing.T) {
	before := map[string]any{}
	p := control.Proposal{Operation: "settings.apply"}
	for _, key := range []string{"one", "two", "three", "four", "five"} {
		before[key] = strings.Repeat("x", 8192)
		p.Edits = append(p.Edits, control.Edit{Key: "/" + key, Op: "unset"})
	}
	raw, _ := json.Marshal(before)
	text := string(raw)
	if undoOffer(commandJournal{CommandID: randomID(), Applied: &p, BeforeRaw: &text}) != nil {
		t.Fatal("oversized optional undo review would block the result upload")
	}
}
