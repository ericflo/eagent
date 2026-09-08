package boundedfile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundedConfinedRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata")
	raw := []byte("exact native bytes\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path, int64(len(raw)))
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("read: %q %v", got, err)
	}
	if _, err := Read(path, int64(len(raw)-1)); err == nil {
		t.Fatal("oversized file accepted")
	}
	if _, err := Read(dir, 1024); err == nil {
		t.Fatal("directory accepted")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRoot(root, "escape", 1024); err == nil {
		t.Fatal("escaped root")
	}
}
