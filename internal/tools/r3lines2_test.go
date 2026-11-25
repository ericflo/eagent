package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// A line far longer than the reader's 1 MB buffer, a blank line,
// and a torn tail (a file still being written) keep search hits and reads
// on the same file:line, and offsets on rune boundaries.
func TestALongLineABlankLineAndAFileStillBeingWrittenReadTheSame(t *testing.T) {
	long := strings.Repeat("é", 700_000) + " needle " + strings.Repeat("ü", 10)
	a := r3Archive(t, r3User("first"), "", r3User(long), r3User("after the long one"))
	path := filepath.Join(a.Path, r3File)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"seq":99,"type":"user.message","data":{"text":"torn needle`)
	f.Close()

	hits := r3Hits(t, a, "needle")
	if len(hits) != 1 || hits[3] == "" {
		t.Fatalf("hits %v", hits)
	}
	if hits := r3Hits(t, a, "after the long"); hits[4] == "" {
		t.Fatalf("hits %v", hits)
	}
	if out, _ := a.Read(r3File, 5, 5, false, -1, 24000); !strings.Contains(out, "has only 4 lines") {
		t.Fatalf("torn line read as a line: %q", out)
	}
	out, _ := a.Read(r3File, 4, 4, false, -1, 24000)
	if !strings.HasPrefix(out, r3File+":4 ") {
		t.Fatalf("line 4 reads %q", out)
	}
	// Follow the hit's offset.
	h := hits[3]
	i := strings.Index(h, "offset=")
	if i < 0 {
		t.Fatalf("no offset in %q", h)
	}
	var off int
	for _, c := range h[i+len("offset="):] {
		if c < '0' || c > '9' {
			break
		}
		off = off*10 + int(c-'0')
	}
	piece, _ := a.Read(r3File, 3, 0, false, off, 24000)
	if !strings.Contains(piece, "needle") || !utf8.ValidString(piece) {
		t.Fatalf("offset %d does not read the match (valid utf8 %v): %q", off, utf8.ValidString(piece), piece[:min(200, len(piece))])
	}
	// Paging the long line in raw mode stays raw and on rune boundaries.
	p1, _ := a.Read(r3File, 3, 3, true, -1, 24000)
	if !strings.Contains(p1, "raw=true") || !utf8.ValidString(p1) {
		t.Fatalf("raw first piece: %q", p1[len(p1)-200:])
	}
}
