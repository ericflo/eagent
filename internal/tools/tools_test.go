package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfinement(t *testing.T) {
	root := t.TempDir()
	f := Files{Root: root}
	if _, err := f.Resolve("src/a.go", true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resolve(filepath.Join(root, "b"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resolve("../../../../../../etc/passwd", true); err == nil {
		t.Fatal("write escape accepted")
	}
	if _, err := f.Resolve("/etc/passwd", true); err == nil {
		t.Fatal("absolute write outside accepted")
	}
	if _, err := f.Resolve("/etc/passwd", false); err != nil {
		t.Fatal("reads may go anywhere")
	}
	if _, err := f.Resolve(filepath.Join(os.TempDir(), "x"), true); err != nil {
		t.Fatal("temp dir should be writable")
	}
	if _, err := (Files{Root: root, AllowOutside: true}).Resolve("/etc/passwd", true); err != nil {
		t.Fatal("AllowOutside should permit")
	}
}

func TestWriteReadEdit(t *testing.T) {
	root := t.TempDir()
	f := Files{Root: root}
	if _, err := f.WriteFile("dir/a.txt", "one\ntwo\nthree\n"); err != nil {
		t.Fatal(err)
	}
	out, err := f.ReadFile("dir/a.txt", 2, 1, 0)
	if err != nil || !strings.Contains(out, "two") || strings.Contains(out, "three") {
		t.Fatalf("window read = %q %v", out, err)
	}
	if _, err := f.EditFile("dir/a.txt", "two", "2", false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.EditFile("dir/a.txt", "missing", "x", false); err == nil {
		t.Fatal("missing text accepted")
	}
	f.WriteFile("dir/b.txt", "a a a")
	if _, err := f.EditFile("dir/b.txt", "a", "b", false); err == nil {
		t.Fatal("ambiguous edit accepted")
	}
	if _, err := f.EditFile("dir/b.txt", "a", "b", true); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "dir/b.txt"))
	if string(raw) != "b b b" {
		t.Fatalf("replace_all = %q", raw)
	}
	big := strings.Repeat("line\n", 10000)
	f.WriteFile("big.txt", big)
	out, _ = f.ReadFile("big.txt", 0, 0, 1000)
	if len(out) > 1300 || !strings.Contains(out, "truncated") {
		t.Fatalf("large file not truncated: %d", len(out))
	}
}

func TestTruncateKeepsHeadAndTailRuneSafe(t *testing.T) {
	s := strings.Repeat("é", 1000)
	out := Truncate(s, 300)
	if !strings.Contains(out, "omitted") || len(out) > 420 {
		t.Fatalf("truncate = %d chars", len(out))
	}
	for _, r := range out {
		if r == '�' {
			t.Fatal("split a rune")
		}
	}
	if Truncate("short", 100) != "short" {
		t.Fatal("short text changed")
	}
}
