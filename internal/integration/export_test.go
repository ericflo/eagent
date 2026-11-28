package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// `eagent artifact export SESSION --output DIR` writes the archive to a new
// directory and never over one that exists. (It reserved the directory
// first and then renamed onto it, which Go refuses, so every export failed.)
func TestExportWritesANewDirectoryAndNeverReplacesOne(t *testing.T) {
	project := t.TempDir()
	sess, err := store.Create(store.Root(project), time.UnixMilli(1788800000000))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: sess.Current(), Index: 0, Reason: "new"}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "export me"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	sess.Close()

	out := filepath.Join(t.TempDir(), "nested", "archive")
	if err := Export(context.Background(), project, sess.ID, "test", out); err != nil {
		t.Fatalf("export: %v", err)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) == 0 {
		t.Fatalf("the archive directory is empty or missing: %v", err)
	}
	// Nothing is left beside it.
	siblings, _ := os.ReadDir(filepath.Dir(out))
	if len(siblings) != 1 {
		t.Fatalf("export left %d entries beside the archive", len(siblings))
	}
	// A second export to the same place is refused and changes nothing.
	marker := filepath.Join(out, "marker")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Export(context.Background(), project, sess.ID, "test", out); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a second export to the same directory: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the existing directory was replaced")
	}
	siblings, _ = os.ReadDir(filepath.Dir(out))
	if len(siblings) != 1 {
		t.Fatalf("a refused export left %d entries beside the archive", len(siblings))
	}
}
