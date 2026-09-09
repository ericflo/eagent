package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// read_file never pulls a device, a pipe, or a huge file into memory.
func TestReadFileRefusesDevicesAndHugeFiles(t *testing.T) {
	root := t.TempDir()
	f := Files{Root: root, AllowOutside: true}
	if _, err := f.ReadFile("/dev/zero", 0, 0, 1<<20); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("/dev/zero: %v", err)
	}
	big := filepath.Join(root, "big.bin")
	fh, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := fh.Truncate(readCap + 1); err != nil { // sparse: costs no disk
		t.Fatal(err)
	}
	fh.Close()
	if _, err := f.ReadFile("big.bin", 0, 0, 1<<20); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("huge file: %v", err)
	}
	if _, err := f.EditFile("big.bin", "a", "b", false); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("huge edit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("fine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := f.ReadFile("ok.txt", 0, 0, 1<<20); err != nil || !strings.Contains(got, "fine") {
		t.Fatalf("ordinary file: %q %v", got, err)
	}
}

// A tool-call id from the endpoint is not a path: spilled output stays in
// the outputs directory whatever the id contains.
func TestSpillNeverLeavesItsDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "outputs")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{strings.Repeat("../", 6) + strings.TrimPrefix(outside, "/") + "/planted", "..", ".", "/etc/x", "call_ok-1"} {
		path, err := Spill(dir, id, "text")
		if err != nil {
			t.Fatalf("%q: %v", id, err)
		}
		if filepath.Dir(path) != dir {
			t.Fatalf("%q spilled to %s", id, path)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("something was written outside: %v", entries)
	}
	if safeBase("call_ok-1") != "call_ok-1" || safeBase("...") == "" || strings.ContainsAny(safeBase("a/b\\c"), "/\\") {
		t.Fatal("safeBase")
	}
}

// Concurrent edits to one file never lose one another: the mutation is
// serialised, so every caller's replacement lands.
func TestConcurrentEditsAreSerialised(t *testing.T) {
	root := t.TempDir()
	f := Files{Root: root}
	var body strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&body, "line %04d\n", i)
	}
	if _, err := f.WriteFile("data.txt", body.String()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			old := fmt.Sprintf("line %04d\n", i*100)
			if _, err := f.EditFile("data.txt", old, fmt.Sprintf("edited %d\n", i), false); err != nil {
				t.Errorf("edit %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	got, _ := f.ReadFile("data.txt", 0, 0, 0)
	for i := 0; i < 8; i++ {
		if !strings.Contains(got, fmt.Sprintf("edited %d\n", i)) {
			t.Fatalf("edit %d was lost", i)
		}
	}
}

// A symlink inside the project that points outside does not carry a write
// with it: containment is judged on the real path.
func TestWriteThroughSymlinkIsRefused(t *testing.T) {
	// The write rule exempts the system temp dir, so the victim must live
	// outside it: point TMPDIR inside the project for the duration.
	outside, err := os.MkdirTemp("", "eagent-outside-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	root := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(root, "tmp"))
	target := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link.txt")); err != nil {
		t.Skip("no symlinks here")
	}
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	f := Files{Root: root}
	if _, err := f.WriteFile("link.txt", "clobbered"); err == nil {
		t.Fatal("a write through a symlink to outside the project was allowed")
	}
	if _, err := f.WriteFile("dirlink/new.txt", "x"); err == nil {
		t.Fatal("a write into a symlinked outside directory was allowed")
	}
	// A dangling link: the target does not exist yet, and the write would
	// create it wherever the link points.
	dangling := filepath.Join(outside, "not-yet.txt")
	if err := os.Symlink(dangling, filepath.Join(root, "dangling.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteFile("dangling.txt", "created outside"); err == nil {
		t.Fatal("a write through a dangling symlink to outside the project was allowed")
	}
	if _, err := os.Stat(dangling); err == nil {
		t.Fatal("the dangling link's outside target was created")
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/inside.txt", filepath.Join(root, "inside-link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteFile("inside-link.txt", "fine"); err != nil {
		t.Fatalf("a dangling link that stays inside the project must still work: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "sub", "inside.txt")); string(got) != "fine" {
		t.Fatal("the in-project link's target was not written")
	}
	// A chain longer than the resolver's hop bound must fail closed, not
	// pass as "inside" because the last unresolved link sits in the project.
	prev := filepath.Join(outside, "chain-end")
	for i := 0; i < 36; i++ {
		link := filepath.Join(root, fmt.Sprintf("l%d", i))
		if err := os.Symlink(prev, link); err != nil {
			t.Fatal(err)
		}
		prev = link
	}
	if _, err := f.WriteFile("l35", "through the chain"); err == nil {
		t.Fatal("a 36-link chain to outside the project was allowed")
	}
	if _, err := os.Stat(filepath.Join(outside, "chain-end")); err == nil {
		t.Fatal("the chain's outside target was created")
	}
	if got, _ := os.ReadFile(target); string(got) != "keep" {
		t.Fatal("the outside file was changed")
	}
	if _, err := f.WriteFile("sub/ok.txt", "fine"); err != nil {
		t.Fatalf("an ordinary nested write must still work: %v", err)
	}
	if _, err := f.ReadFile("link.txt", 0, 0, 0); err != nil {
		t.Fatalf("reads through symlinks are still allowed: %v", err)
	}
}

func TestFilesResolveAgainstWorkingDirectory(t *testing.T) {
	// The OS temp dir is always writable, so the fixture must live outside
	// it: point TMPDIR at a scratch corner and keep the directories elsewhere.
	base := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(base, "scratch"))
	root, other, stranger, sub := filepath.Join(base, "root"), filepath.Join(base, "other"), filepath.Join(base, "stranger"), filepath.Join(base, "root", "sub")
	for _, d := range []string{filepath.Join(base, "scratch"), root, other, stranger, sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := Files{Root: root}
	if got, _ := f.Resolve("a.txt", false); got != filepath.Join(root, "a.txt") {
		t.Fatalf("default base: %q", got)
	}
	if got, _ := f.At(sub).Resolve("a.txt", false); got != filepath.Join(sub, "a.txt") {
		t.Fatalf("base not honoured: %q", got)
	}
	// Writes may land in the project, in the working directory the actor
	// chose, or in the temp dir, and nowhere else.
	if _, err := f.At(other).Resolve("out.txt", true); err != nil {
		t.Fatalf("write in the working directory refused: %v", err)
	}
	if _, err := f.At(other).Resolve(filepath.Join(root, "in.txt"), true); err != nil {
		t.Fatalf("write in the project refused from elsewhere: %v", err)
	}
	if _, err := f.At(other).Resolve(filepath.Join(stranger, "x.txt"), true); err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("write outside both allowed: %v", err)
	}
	if _, err := f.Resolve(filepath.Join(stranger, "x.txt"), true); err == nil || strings.Contains(err.Error(), "working directory") {
		t.Fatalf("message should name only the project when no cd happened: %v", err)
	}
	if out, err := f.At(sub).ListDir(""); err != nil || out != "(empty directory)" {
		t.Fatalf("list_dir with no path should list the working directory: %q %v", out, err)
	}
}
