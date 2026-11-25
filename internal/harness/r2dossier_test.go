package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/tools"
)

// Tests for the second review of the dossier: every byte of a long event is
// reachable, the map is counted in distinct places that exist, and one
// deadline bounds the whole pass. Each is named TestR2Dossier...

// r2LongMessage is a pasted message of about 33 KB, with runes of every
// width, line breaks, and a last sentence of its own.
func r2LongMessage() string {
	var b strings.Builder
	for i := 0; b.Len() < 33000; i++ {
		fmt.Fprintf(&b, "Requirement %d: the café's 日本 order must ship 🚀 by Friday.\n", i)
	}
	b.WriteString("The very last sentence says the deploy goes out on ZEBRAFINCH day.")
	return b.String()
}

var (
	r2Piece    = regexp.MustCompile(`^(\S+:\d+) \[offset (\d+) of (\d+)\] `)
	r2Continue = regexp.MustCompile(`from=(\d+) offset=(\d+)`)
)

// r2Follow reads one line whole through read, starting with a plain read
// of it and then following what each result says to pass for the rest.
// It returns the line's text put back together from the pieces.
func r2Follow(t *testing.T, read func(args string) string, file string, line int, raw bool, bound int) string {
	t.Helper()
	args := fmt.Sprintf(`{"file":%q,"from":%d,"to":%d,"raw":%v}`, file, line, line, raw)
	var whole strings.Builder
	next := 0
	for i := 0; ; i++ {
		if i > 10 {
			t.Fatal("still not at the end of the line after 10 reads")
		}
		out := read(args)
		if len(out) > bound {
			t.Fatalf("one read returned %d characters, more than %d", len(out), bound)
		}
		if !utf8.ValidString(out) {
			t.Fatal("a read split a rune")
		}
		first, rest, _ := strings.Cut(out, "\n")
		m := r2Piece.FindStringSubmatch(first)
		if m == nil {
			// A line that fit, or the readable form's shortened line: it
			// must say how to read the rest.
			c := r2Continue.FindStringSubmatch(out)
			if c == nil || i > 0 {
				t.Fatalf("read %d (%s) gives no piece and no way on:\n%.400s\n...\n%s", i, args, out, out[max(0, len(out)-400):])
			}
			args = fmt.Sprintf(`{"file":%q,"from":%s,"offset":%s,"raw":%v}`, file, c[1], c[2], raw)
			continue
		}
		off, _ := strconv.Atoi(m[2])
		if off != next {
			t.Fatalf("a piece starts at %d, want %d", off, next)
		}
		piece := first[len(m[0]):]
		whole.WriteString(piece)
		next = off + len(piece)
		total, _ := strconv.Atoi(m[3])
		c := r2Continue.FindStringSubmatch(rest)
		if next == total {
			if c != nil || strings.TrimSpace(rest) != "" {
				t.Fatalf("the last piece does not end cleanly: %q", rest)
			}
			return whole.String()
		}
		if c == nil || c[1] != strconv.Itoa(line) || c[2] != strconv.Itoa(next) {
			t.Fatalf("a piece cut at %d of %d says %q", next, total, rest)
		}
		if raw && !strings.Contains(rest, "raw=true") {
			t.Fatalf("a raw piece does not say to read on raw: %q", rest)
		}
		args = fmt.Sprintf(`{"file":%q,"from":%d,"offset":%d,"raw":%v}`, file, line, next, raw)
	}
}

// A pasted message longer than one read is read to its last byte in
// bounded pieces, raw and readable, in the main loop and in the dossier
// pass, and a read past its end says so.
func TestR2DossierSessionReadReachesTheEndOfALongLine(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	text := r2LongMessage()
	var file string
	var ev event.Event
	s.rt.sync(func() {
		file = s.rt.sess.Current()
		ev = s.rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text}))
	})
	s.settled()
	loop := func(args string) string {
		out, isErr, _ := s.rt.runTool(context.Background(), &turnCalls{}, tc("session_read", args))
		if isErr {
			t.Fatalf("%s: %s", args, out)
		}
		return out
	}
	var read int
	pass := func(args string) string {
		out, isErr := s.rt.dossierLookup(tc("session_read", args), &read, 1<<30, &dossierResult{})
		if isErr {
			t.Fatalf("%s: %s", args, out)
		}
		return out
	}
	for _, c := range []struct {
		name  string
		read  func(string) string
		bound int
	}{{"loop", loop, loopReadChars + 400}, {"dossier", pass, dossierReadChars + 400}} {
		raw := r2Follow(t, c.read, file, ev.Source.Line, true, c.bound)
		var back event.Event
		if err := json.Unmarshal([]byte(raw), &back); err != nil {
			t.Fatalf("%s: the raw pieces do not make the event: %v", c.name, err)
		}
		var d event.UserMessageData
		if err := back.Decode(&d); err != nil || d.Text != text {
			t.Fatalf("%s: the raw pieces do not hold the message whole (%d of %d characters)", c.name, len(d.Text), len(text))
		}
		readable := r2Follow(t, c.read, file, ev.Source.Line, false, c.bound)
		if readable != tools.Summarize(ev, 0) || !strings.HasSuffix(readable, "ZEBRAFINCH day.") {
			t.Fatalf("%s: the readable pieces are not the line whole (%d characters)", c.name, len(readable))
		}
	}
	// Past the end: a message, not an empty result.
	total := len(tools.Summarize(ev, 0))
	out := loop(fmt.Sprintf(`{"file":%q,"from":%d,"offset":%d}`, file, ev.Source.Line, total))
	if !strings.Contains(out, "past its end") {
		t.Fatalf("a read past the end of the line says %q", out)
	}
}

// A search hit inside a long event says where in it the match is, and a
// read from there shows it.
func TestR2DossierSearchHitInALongLineCanBeRead(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	var file string
	var ev event.Event
	s.rt.sync(func() {
		file = s.rt.sess.Current()
		ev = s.rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: r2LongMessage()}))
	})
	s.settled()
	out, isErr, _ := s.rt.runTool(context.Background(), &turnCalls{}, tc("session_search", `{"query":"zebrafinch"}`))
	if isErr {
		t.Fatal(out)
	}
	hit := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, fmt.Sprintf("%s:%d ", file, ev.Source.Line)) {
			hit = l
		}
	}
	c := r2Continue.FindStringSubmatch(hit)
	if c == nil || c[1] != strconv.Itoa(ev.Source.Line) {
		t.Fatalf("the hit gives no place to read the match from:\n%s", out)
	}
	got, isErr, _ := s.rt.runTool(context.Background(), &turnCalls{}, tc("session_read", fmt.Sprintf(`{"file":%q,"from":%s,"offset":%s}`, file, c[1], c[2])))
	if isErr || !strings.Contains(got, "ZEBRAFINCH") || len(got) > loopReadChars+400 {
		t.Fatalf("reading from the hit's offset (%d characters) does not show the match:\n%.600s", len(got), got)
	}
}
