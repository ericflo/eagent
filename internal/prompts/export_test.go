package prompts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

func TestExportSharesSettingsLockAndPreservesExistingOverrides(t *testing.T) {
	project := t.TempDir()
	editor, err := config.LockEditor(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	defer editor.Close()
	done := make(chan error, 1)
	go func() { _, err := Export(project); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("export bypassed settings lock: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	if err := os.MkdirAll(Dir(project), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(project), "PERSONA.md")
	if err := os.WriteFile(path, []byte("reviewed remote override"), 0600); err != nil {
		t.Fatal(err)
	}
	editor.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("export did not finish after lock release")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "reviewed remote override" {
		t.Fatal("export replaced an existing override", err)
	}
}
