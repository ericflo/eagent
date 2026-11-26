package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A new session whose first file cannot be made lets its lock go: the
// directory is not left looking like a session some run holds.
func TestANewSessionThatCannotMakeItsFirstFileLetsTheLockGo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1788800000000)
	// A link to nowhere where the first file goes: it reads as absent and
	// cannot be created.
	if err := os.Symlink(filepath.Join(path, "missing", "x"), filepath.Join(path, NewID(now)+".jsonl")); err != nil {
		t.Fatal(err)
	}
	s := &Session{ID: "s", Path: path}
	if err := s.start(now); err == nil {
		t.Fatal("the first file was made over the link")
	}
	if Alive(path) {
		t.Fatal("the failed start still holds the session's lock")
	}
}
