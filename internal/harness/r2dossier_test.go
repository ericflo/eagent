package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

// A briefing that cites one place three times is sent back, saying a
// place cited again is still one place, and kept as a summary when it
// comes back the same.
func TestR2DossierOnePlaceThreeTimesIsNotAMap(t *testing.T) {
	var mu sync.Mutex
	var sentBack string
	s, _ := audit2Dossier(t, func(n int, msgs []map[string]any) reply {
		p := places(dossierRequest(msgs))[0]
		if n == 2 {
			mu.Lock()
			sentBack = lastUserText(msgs)
			mu.Unlock()
		}
		return reply{text: briefing([]string{p, p, p})}
	})
	d := s.dossiers()[0]
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(sentBack, "That cannot be used: its MAP cites 1 place in the record") || !strings.Contains(sentBack, "the same place cited again is still one place") {
		t.Fatalf("sent back with %q", sentBack)
	}
	if d.By != "eagent" || !d.NoMap || d.Calls != 2 || !strings.HasPrefix(d.Text, "(eagent checked this briefing and it is not a map") {
		t.Fatalf("dossier by %q nomap=%v after %d calls:\n%s", d.By, d.NoMap, d.Calls, d.Text)
	}
}

// Places that do not exist do not count toward a map: a briefing that
// cites only three of them twice is kept as a summary, with them named.
func TestR2DossierNonexistentPlacesDoNotMakeAMap(t *testing.T) {
	var mu sync.Mutex
	var sentBack string
	s, _ := audit2Dossier(t, func(n int, msgs []map[string]any) reply {
		if n == 2 {
			mu.Lock()
			sentBack = lastUserText(msgs)
			mu.Unlock()
		}
		return reply{text: briefing([]string{"1111111111111.jsonl:5", "1111111111111.jsonl:6", "1111111111111.jsonl:7"})}
	})
	d := s.dossiers()[0]
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(sentBack, "its MAP cites 0 places in the record") || !strings.Contains(sentBack, "these places do not exist in the record: 1111111111111.jsonl:5") {
		t.Fatalf("sent back with %q", sentBack)
	}
	if !d.NoMap || !strings.HasPrefix(d.Text, "(eagent checked this briefing and it is not a map") || !strings.Contains(d.Text, "disregard them: 1111111111111.jsonl:5, 1111111111111.jsonl:6, 1111111111111.jsonl:7") {
		t.Fatalf("dossier by %q nomap=%v:\n%s", d.By, d.NoMap, d.Text)
	}
}

// What counts is distinct places that exist, by file and first line: a
// range and a line it starts with are one place; a line inside a range
// is another.
func TestR2DossierCitationsCountDistinctPlaces(t *testing.T) {
	lines := map[string]int{"1700000000000.jsonl": 30, "1700000000001.jsonl": 5}
	for _, c := range []struct {
		text string
		n    int
		bad  int
	}{
		{"1700000000000.jsonl:10 1700000000000.jsonl:10 1700000000000.jsonl:10", 1, 0},
		{"1700000000000.jsonl:10-20 1700000000000.jsonl:10 1700000000000.jsonl:10-10", 1, 0},
		{"1700000000000.jsonl:10-20 1700000000000.jsonl:12 1700000000001.jsonl:10", 2, 1},
		{"1700000000000.jsonl:31 1700000000000.jsonl:31 1700000000002.jsonl:1", 0, 2},
		{"1700000000000.jsonl:1 1700000000000.jsonl:2 1700000000001.jsonl:5", 3, 0},
	} {
		n, bad := checkCitations(c.text, lines)
		if n != c.n || len(bad) != c.bad {
			t.Errorf("%q: %d places, %d bad; want %d, %d", c.text, n, len(bad), c.n, c.bad)
		}
	}
}

// r2DossierTime scales the dossier pass for a test: pass of reading time,
// write more reserved for the writing.
func r2DossierTime(t *testing.T, pass, write time.Duration) {
	oldPass := dossierPassTime
	dossierPassTime = pass
	t.Cleanup(func() { dossierPassTime = oldPass })
	oldWrite := dossierWriteTime
	dossierWriteTime = write
	t.Cleanup(func() { dossierWriteTime = oldWrite })
}

// after is a reply's block that opens d from now.
func after(d time.Duration) <-chan struct{} {
	ch := make(chan struct{})
	time.AfterFunc(d, func() { close(ch) })
	return ch
}

// The reviewer's case, scaled: the pass's reading time is 1.5 s and every
// call is a lookup that takes 3.5 s. One deadline bounds the whole pass, so
// the harness writes the briefing and the loop answers again by the time
// the reading and the writing allowed (1.5 s + 1 s), not two calls later
// (7 s). The times are long enough that a busy machine does not decide the
// outcome.
func TestR2DossierPassIsBoundedAsAWhole(t *testing.T) {
	r2DossierTime(t, 1500*time.Millisecond, time.Second)
	var mu sync.Mutex
	var first time.Time
	var told []string
	s, _ := audit2Dossier(t, func(n int, msgs []map[string]any) reply {
		mu.Lock()
		if n == 1 {
			first = time.Now()
		}
		told = append(told, lastText(msgs))
		mu.Unlock()
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"PR"}`)}, block: after(3500 * time.Millisecond)}
	})
	took := time.Since(first)
	if d := s.dossiers()[0]; d.By != "harness" {
		t.Fatalf("dossier by %q", d.By)
	}
	if took > 3300*time.Millisecond {
		t.Fatalf("the pass took %s; it is bounded at 2.5s", took)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(told) != 2 || !strings.Contains(told[1], "Write the briefing now") {
		t.Fatalf("the writer was not told to write in its reserved time: %q", told)
	}
}

// A lookup still running when the reading time is over is cut short so
// the writing keeps its reserved time, and the model's briefing is kept.
func TestR2DossierWritingKeepsItsReservedTime(t *testing.T) {
	r2DossierTime(t, 500*time.Millisecond, 2*time.Second)
	var mu sync.Mutex
	var first time.Time
	s, _ := audit2Dossier(t, func(n int, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		if n == 1 {
			first = time.Now()
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"PR"}`)}, block: after(10 * time.Second)}
		}
		if !strings.Contains(lastText(msgs), "Write the briefing now") {
			return reply{text: "not told to write"}
		}
		return reply{text: briefing(places(dossierRequest(msgs))[:3])}
	})
	took := time.Since(first)
	if d := s.dossiers()[0]; d.By != "eagent" || d.NoMap {
		t.Fatalf("dossier by %q nomap=%v:\n%s", d.By, d.NoMap, d.Text)
	}
	// The lookup is cut at 1.5 s (the reading time and half the writing
	// time), and the writing has a second left.
	if took > 2500*time.Millisecond {
		t.Fatalf("the pass took %s; it is bounded at 2.5s", took)
	}
}
