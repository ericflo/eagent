package filelock

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCreateFileNeverReplacesConcurrentWritersOrDanglingLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.json")
	values := [][]byte{bytes.Repeat([]byte("a"), 10000), bytes.Repeat([]byte("b"), 10000)}
	errors := make(chan error, 2)
	var group sync.WaitGroup
	for _, raw := range values {
		group.Add(1)
		go func() { defer group.Done(); errors <- CreateFile(path, raw, 0600) }()
	}
	group.Wait()
	close(errors)
	winners := 0
	for err := range errors {
		if err == nil {
			winners++
		} else if !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("got %d winning creators", winners)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, values[0]) && !bytes.Equal(raw, values[1]) {
		t.Fatal("published partial or interleaved file")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "absent")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if err := CreateFile(path, []byte("replacement"), 0600); !os.IsExist(err) {
		t.Fatalf("replaced dangling symlink: %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("wrote through dangling symlink")
	}
}
