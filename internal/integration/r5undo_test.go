package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/settings"
)

// handmade is a bundle written on this machine whose agent profile exists
// nowhere else.
const handmade = `{"preset":"glm","relay":{"profiles":{"solo":{"agent":"claude","args":["--verbose"]}}}}`

func handmadeBundle(t *testing.T) (*settings.Service, control.Grant, string) {
	t.Helper()
	s := &settings.Service{Project: t.TempDir()}
	path := config.BundlePath(s.Project, "handmade")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(handmade), 0o644); err != nil {
		t.Fatal(err)
	}
	return s, s.Grant(), path
}

func bundleBytes(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "<absent>"
	}
	return string(raw)
}

func TestUndoOfARemoteBundleDeleteRestoresItsProfilesExactly(t *testing.T) {
	s, g, path := handmadeBundle(t)
	del := executeExpected(t, s, g, reviewedCommand(t, s, g, control.Proposal{Operation: "bundle.delete", Parameters: map[string]any{"name": "handmade"}}), "succeeded")
	if bundleBytes(path) != "<absent>" {
		t.Fatal("not deleted")
	}
	undo := executeExpected(t, s, g, reviewedUndo(t, s, g, del), "succeeded")
	if got := bundleBytes(path); got != handmade {
		t.Fatalf("undo restored %q, want the bytes that were here", got)
	}
	// Undo of the undo deletes it again, and undoing that restores it again.
	again := executeExpected(t, s, g, reviewedUndo(t, s, g, undo), "succeeded")
	if bundleBytes(path) != "<absent>" {
		t.Fatal("undo of the undo did not delete it again")
	}
	executeExpected(t, s, g, reviewedUndo(t, s, g, again), "succeeded")
	if got := bundleBytes(path); got != handmade {
		t.Fatalf("third undo restored %q", got)
	}
}

func TestUndoOfARemoteBundleOverwriteRestoresItsProfilesExactly(t *testing.T) {
	s, g, path := handmadeBundle(t)
	save := executeExpected(t, s, g, reviewedCommand(t, s, g, control.Proposal{Operation: "bundle.save", Parameters: map[string]any{"name": "handmade", "config_json": `{"preset":"glm"}`}}), "succeeded")
	if strings.Contains(bundleBytes(path), "solo") {
		t.Fatal("not overwritten")
	}
	executeExpected(t, s, g, reviewedUndo(t, s, g, save), "succeeded")
	if got := bundleBytes(path); got != handmade {
		t.Fatalf("undo restored %q, want the bytes that were here", got)
	}
}

// A remote bundle.save with a profile this machine does not have stays
// refused, and an undo can only restore what eagent's own record holds.
func TestAnUndoRequestCannotBringItsOwnContent(t *testing.T) {
	s, g, path := handmadeBundle(t)
	evil := `{"preset":"glm","relay":{"profiles":{"solo":{"agent":"claude","args":[";","run-shell","touch /tmp/pwned"]}}}}`
	executeExpected(t, s, g, reviewedCommand(t, s, g, control.Proposal{Operation: "bundle.save", Parameters: map[string]any{"name": "other", "config_json": evil}}), "rejected")
	del := executeExpected(t, s, g, reviewedCommand(t, s, g, control.Proposal{Operation: "bundle.delete", Parameters: map[string]any{"name": "handmade"}}), "succeeded")
	undo := reviewedUndo(t, s, g, del)
	undo.Proposal.Parameters["config_json"] = evil
	undo.Proposal.Parameters["name"] = "other"
	j, _ := executeCommand(context.Background(), s, g, undo, false)
	if strings.Contains(bundleBytes(path)+bundleBytes(config.BundlePath(s.Project, "other")), "run-shell") {
		t.Fatalf("an undo wrote content it was sent (%s)", j.Status)
	}
	if j.Status != "rejected" || bundleBytes(path) != "<absent>" {
		t.Fatalf("an undo carrying content of its own was %s; the bundle is %q", j.Status, bundleBytes(path))
	}
	// The same undo as reviewed still restores the recorded bytes.
	executeExpected(t, s, g, reviewedUndo(t, s, g, del), "succeeded")
	if bundleBytes(path) != handmade {
		t.Fatalf("undo restored %q", bundleBytes(path))
	}
}

func TestAnUndoCannotNameARecordItDoesNotOwn(t *testing.T) {
	s, g, path := handmadeBundle(t)
	// A real record of another project, under this project's commands.
	other, og, _ := handmadeBundle(t)
	foreign := executeExpected(t, other, og, reviewedCommand(t, other, og, control.Proposal{Operation: "bundle.delete", Parameters: map[string]any{"name": "handmade"}}), "succeeded")
	raw, err := os.ReadFile(filepath.Join(stateDir(other.Project), "commands", foreign.CommandID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir(s.Project), "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir(s.Project), "commands", foreign.CommandID+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	offer := reviewedUndo(t, other, og, foreign).Proposal.Parameters
	for _, id := range []any{foreign.CommandID, "no-such-command", "../" + foreign.CommandID, ""} {
		q := reviewedCommand(t, s, g, control.Proposal{Operation: "settings.undo", Parameters: map[string]any{"command_id": id, "restore_sha256": offer["restore_sha256"]}})
		executeExpected(t, s, g, q, "rejected")
	}
	if bundleBytes(path) != handmade {
		t.Fatal("an undo of a record it does not own touched the bundle")
	}
}

func TestAReplayedUndoRestoresOnlyOnce(t *testing.T) {
	s, g, path := handmadeBundle(t)
	del := executeExpected(t, s, g, reviewedCommand(t, s, g, control.Proposal{Operation: "bundle.delete", Parameters: map[string]any{"name": "handmade"}}), "succeeded")
	undo := reviewedUndo(t, s, g, del)
	executeExpected(t, s, g, undo, "succeeded")
	// Changed here after the undo: neither a replay of the same command nor
	// a second undo of the same record may put the old bytes back.
	if err := os.WriteFile(path, []byte(`{"preset":"glm"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if j, err := executeCommand(context.Background(), s, g, undo, true); err != nil || j.Status != "succeeded" {
		t.Fatalf("replay: %s %v", j.Status, err)
	}
	j, _ := executeCommand(context.Background(), s, g, reviewedUndo(t, s, g, del), false)
	if j.Status == "succeeded" {
		t.Fatal("a second undo of the same record succeeded")
	}
	if got := bundleBytes(path); got != `{"preset":"glm"}` {
		t.Fatalf("a replayed undo wrote again: %q", got)
	}
}
