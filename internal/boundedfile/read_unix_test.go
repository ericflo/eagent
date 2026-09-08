//go:build unix

package boundedfile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadRejectsPipeIncludingReplacementAtOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := read(path, 1024, os.Stat, func(name string, flags int, mode os.FileMode) (*os.File, error) {
			if err := os.Remove(name); err != nil {
				return nil, err
			}
			if err := syscall.Mkfifo(name, 0600); err != nil {
				return nil, err
			}
			return os.OpenFile(name, flags, mode)
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted replacement pipe")
		}
	case <-time.After(time.Second):
		t.Fatal("read blocked waiting for a pipe writer")
	}
	if _, err := Read(path, 1024); err == nil {
		t.Fatal("accepted pipe")
	}
}
