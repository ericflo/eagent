package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

const r3File = "1788740857293.jsonl"

// r3Archive writes lines (an event, or "" for a blank line) to one record
// file, the way the store writes them, and returns the archive over it.
func r3Archive(t *testing.T, lines ...any) Archive {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	for i, l := range lines {
		switch v := l.(type) {
		case string:
			b.WriteString(v + "\n")
		case event.Event:
			v.Seq = int64(i + 1)
			v.Time = time.Date(2026, 11, 25, 9, 0, i, 0, time.UTC)
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteString("\n")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, r3File), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return Archive{Path: dir}
}

func r3User(text string) event.Event {
	return event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text})
}

// r3Hits returns the lines a search reports, by file line.
func r3Hits(t *testing.T, a Archive, query string) map[int]string {
	t.Helper()
	out, err := a.Search(query, "", 40)
	if err != nil {
		t.Fatal(err)
	}
	hits := map[int]string{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		rest, ok := strings.CutPrefix(l, r3File+":")
		if !ok {
			continue
		}
		n, _, _ := strings.Cut(rest, " ")
		line, err := strconv.Atoi(n)
		if err == nil {
			hits[line] = l
		}
	}
	return hits
}

// A query matches the text of the event as a person reads it, not its
// JSON: characters JSON escapes, quotes, backslashes and a phrase that runs
// over a line break all match.
func TestSearchMatchesTheTextAPersonSees(t *testing.T) {
	a := r3Archive(t,
		r3User("check `a && b` when x > 0, then fix the <div> that says \"quoted words\""),
		r3User(`the log is in C:\Users\eric\eagent.log`),
		r3User("the first part of the plan\nand the second part"),
		event.New(event.Assistant, event.ActorEagent, event.AssistantData{
			ToolCalls: []event.ToolCall{{ID: "c1", Name: "relay_send", Args: json.RawMessage(`{"text":"wait on <-ctx.Done() & return"}`)}},
		}),
	)
	for query, line := range map[string]int{
		"a && b":                1,
		"x > 0":                 1,
		"<div>":                 1,
		`"quoted words"`:        1,
		`C:\Users\eric`:         2,
		"plan and the second":   3,
		"plan\nand the":         3,
		"<-ctx.Done() & return": 4,
		"WAIT ON <-ctx.done()":  4,
	} {
		hits := r3Hits(t, a, query)
		if _, ok := hits[line]; !ok || len(hits) != 1 {
			t.Errorf("search %q: hits %v, want only line %d", query, hits, line)
		}
	}
}

// A provider's replay payload (signed thinking, encrypted reasoning) is
// not searched: a short word does not match inside it.
func TestSearchDoesNotMatchInsideProviderBlobs(t *testing.T) {
	native := `[{"type":"thinking","thinking":"plan it","signature":"EqQBCkgIBhABGAIiQapiXbugYjwtFixSqlZerrQ"},{"type":"reasoning","encrypted_content":"gAAAAABpapiBUGerrfixjwtsql"}]`
	a := r3Archive(t,
		event.New(event.Assistant, event.ActorEagent, event.AssistantData{Text: "looking now", Native: json.RawMessage(native)}),
		r3User("is there a bug?"),
	)
	for _, q := range []string{"api", "jwt", "sql", "fix", "EqQBCkg"} {
		if hits := r3Hits(t, a, q); len(hits) != 0 {
			t.Errorf("search %q matched inside the provider's payload: %v", q, hits)
		}
	}
	if hits := r3Hits(t, a, "bug"); len(hits) != 1 || hits[2] == "" {
		t.Errorf("search bug: %v, want only line 2", hits)
	}
}

var r3Place = regexp.MustCompile(`from=(\d+) offset=(\d+)( raw=true)?`)

// A hit inside a long event says where the match is, in a form
// session_read takes, and the read shows the match, also when the match is
// one JSON escapes or one that spans a line break.
func TestSearchHitOffsetsReadTheMatch(t *testing.T) {
	long := strings.Repeat("filler words go here. ", 60)
	a := r3Archive(t,
		r3User(long+"then x > 0 && <b>done</b>"+long),
		r3User(long+"the end of one line\nand the start of the next"+long),
		event.New(event.Assistant, event.ActorEagent, event.AssistantData{Text: long, Reasoning: long + "the quokka thought" + long}),
	)
	for query, line := range map[string]int{"x > 0 && <b>": 1, "one line and the start": 2, "quokka thought": 3} {
		hit := r3Hits(t, a, query)[line]
		m := r3Place.FindStringSubmatch(hit)
		if m == nil || m[1] != strconv.Itoa(line) {
			t.Fatalf("search %q: the hit gives no place to read: %q", query, hit)
		}
		got, err := a.Read(r3File, line, 0, m[3] != "", atoi(m[2]), 24000)
		if err != nil {
			t.Fatal(err)
		}
		// The read shows the match within its first few hundred bytes.
		head := got[:min(len(got), 600)]
		if !strings.Contains(r3Norm(head), r3Norm(query)) {
			t.Errorf("search %q: reading from %s does not show the match:\n%s", query, m[0], head)
		}
	}
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// r3Norm reads text as a person does: the line-break mark and the JSON
// escape for one as a break, and any run of spaces as one.
func r3Norm(s string) string {
	s = strings.NewReplacer("⏎", " ", `\n`, " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// A line that was not shortened is not labelled as shortened, in a read
// or in a search.
func TestUnshortenedLinesAreNotCalledShortened(t *testing.T) {
	a := r3Archive(t, r3User("seal "+strings.Repeat("x", 280)), r3User("seal "+strings.Repeat("y", 380)))
	out, err := a.Read(r3File, 1, 2, false, -1, 24000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "shortened") {
		t.Errorf("a read calls lines of 285 and 385 characters shortened:\n%s", out)
	}
	if hit := r3Hits(t, a, "seal")[1]; strings.Contains(hit, "shortened") {
		t.Errorf("a search calls a 285-character line shortened: %q", hit)
	}
	// One that was is.
	b := r3Archive(t, r3User("seal "+strings.Repeat("z", 500)))
	if out, _ := b.Read(r3File, 1, 1, false, -1, 24000); !strings.Contains(out, "[shortened; from=1 offset=0") {
		t.Errorf("a read of a shortened line does not say so:\n%s", out)
	}
}

// When a raw read stops at its limit, the note that says how to go on
// keeps raw=true.
func TestRawReadContinuationStaysRaw(t *testing.T) {
	a := r3Archive(t, r3User(strings.Repeat("a", 300)), r3User(strings.Repeat("b", 300)), r3User(strings.Repeat("c", 300)))
	out, err := a.Read(r3File, 1, 3, true, -1, 900)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "output limit reached") || !strings.Contains(out, "raw=true") {
		t.Errorf("the note in a raw read that stops at its limit is:\n%s", out)
	}
	out, _ = a.Read(r3File, 1, 3, false, -1, 900)
	if strings.Contains(out, "raw=true") {
		t.Errorf("a readable read's note asks for raw:\n%s", out)
	}
}

// A blank line in a file does not move session_read away from the
// file:line each event records.
func TestReadFollowsFileLinesPastABlankLine(t *testing.T) {
	a := r3Archive(t, r3User("first"), "", r3User("third"), r3User("fourth"))
	out, err := a.Read(r3File, 3, 3, false, -1, 24000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, r3File+":3 ") || !strings.Contains(out, "third") || strings.Contains(out, "fourth") {
		t.Errorf("from=3 to=3 reads:\n%s", out)
	}
	out, _ = a.Read(r3File, 3, 0, false, 0, 24000)
	if !strings.Contains(out, ":3 [offset 0") || !strings.Contains(out, "third") {
		t.Errorf("from=3 offset=0 reads:\n%s", out)
	}
	out, _ = a.Read(r3File, 4, 4, false, -1, 24000)
	if !strings.Contains(out, "fourth") {
		t.Errorf("from=4 reads:\n%s", out)
	}
	if out, _ := a.Read(r3File, 5, 5, false, -1, 24000); !strings.Contains(out, "has only 4 lines") {
		t.Errorf("from=5 reads:\n%s", out)
	}
	if out, _ := a.Read(r3File, 2, 2, false, 0, 24000); !strings.Contains(out, "blank") {
		t.Errorf("from=2 offset=0, the blank line, reads:\n%s", out)
	}
	// A search hit's line is the one session_read reads.
	if hit := r3Hits(t, a, "fourth")[4]; hit == "" {
		t.Errorf("search fourth: %v", r3Hits(t, a, "fourth"))
	}
}
