package harness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/state"
)

// partMarker is the line a part of a long report ends with.
var partMarker = regexp.MustCompile(`\n\n_\(part (\d+) of (\d+)\)_$`)

// unsplit takes the parts of a report back to the report: the marker each
// ends with goes, and so do the fence lines added where a code block was
// cut (a closing line at the end of a part, the same opening line at the
// start of the next).
func unsplit(t *testing.T, parts []string) string {
	t.Helper()
	var b strings.Builder
	for i, p := range parts {
		if len(parts) > 1 {
			loc := partMarker.FindStringSubmatchIndex(p)
			if loc == nil {
				t.Fatalf("part %d does not end with its marker: ...%q", i+1, p[max(0, len(p)-40):])
			}
			p = p[:loc[0]]
		}
		if i > 0 {
			if open := reopened(parts[i-1]); open != "" {
				if !strings.HasPrefix(p, open+"\n") {
					t.Fatalf("part %d does not reopen the code block with %q: %q", i+1, open, p[:min(len(p), 40)])
				}
				p = strings.TrimPrefix(p, open+"\n")
			}
		}
		if i < len(parts)-1 {
			if fence := openFenceAtEnd(p); fence != "" {
				// The part closed its block: drop the "\n" + closing fence
				// that was added.
				if !strings.HasSuffix(p, "\n"+fence) {
					t.Fatalf("part %d leaves a %q block open", i+1, fence)
				}
				p = strings.TrimSuffix(p, "\n"+fence)
			}
		}
		b.WriteString(p)
	}
	return b.String()
}

// openFenceAtEnd is the fence of the block still open when the text ends,
// counting the closing line the splitter adds as a close: it reads the
// text without its last line.
func openFenceAtEnd(p string) string {
	i := strings.LastIndexByte(p, '\n')
	if i < 0 {
		return ""
	}
	fence, _ := scanFences(p[:i+1], "", "")
	return fence
}

// reopened is the opening line the next part must begin with, after part
// p (marker and all) when it cut a code block, else "".
func reopened(p string) string {
	if loc := partMarker.FindStringIndex(p); loc != nil {
		p = p[:loc[0]]
	}
	i := strings.LastIndexByte(p, '\n')
	if i < 0 {
		return ""
	}
	_, open := scanFences(p[:i+1], "", "")
	return open
}

// scanFences follows code blocks through text, as CommonMark reads them,
// and returns the fence of the block left open and its opening line.
func scanFences(text, fence, open string) (string, string) {
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimLeft(line, " ")
		if len(line)-len(s) > 3 || s == "" {
			continue
		}
		c := s[0]
		if c != '`' && c != '~' {
			continue
		}
		n := 0
		for n < len(s) && s[n] == c {
			n++
		}
		if n < 3 {
			continue
		}
		if fence == "" {
			if c == '`' && strings.ContainsRune(s[n:], '`') {
				continue
			}
			fence, open = s[:n], strings.TrimSpace(line)
		} else if c == fence[0] && n >= len(fence) && strings.TrimSpace(s[n:]) == "" {
			fence, open = "", ""
		}
	}
	return fence, open
}

// Each part starts with the report's own words and ends with its marker,
// and the parts go back to the report once the markers and the fence lines
// added at the cuts are taken away.
func TestReportPartsStartWithTheReportAndEndWithTheirMarker(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	report := "First line of the report.\n" + strings.Repeat(line, 3000) + "END"
	parts := splitReport(report, phoneReportPart)
	if len(parts) < 2 {
		t.Fatalf("%d parts", len(parts))
	}
	if !strings.HasPrefix(parts[0], "First line of the report.") {
		t.Errorf("part 1 starts %q", parts[0][:30])
	}
	for i, p := range parts {
		if strings.HasPrefix(p, "_(") {
			t.Errorf("part %d starts with its marker", i+1)
		}
		m := partMarker.FindStringSubmatch(p)
		if m == nil || m[1] != itoa(i+1) || m[2] != itoa(len(parts)) {
			t.Errorf("part %d ends %q", i+1, p[len(p)-30:])
		}
		if len(p) > phoneReportPart {
			t.Errorf("part %d is %d bytes", i+1, len(p))
		}
	}
	if got := unsplit(t, parts); got != report {
		t.Fatalf("the parts rejoin to %d bytes, want %d", len(got), len(report))
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// A code block cut between parts is closed with its own fence and opened
// again with the same fence and language, for backtick fences of any
// length and for tilde fences; a fence inside a longer one does not close
// it.
func TestSplitReportReopensCodeBlocksWithTheirFenceAndLanguage(t *testing.T) {
	body := strings.Repeat("+ a line of code\n", 40)
	cases := []struct{ name, open, close string }{
		{"go", "```go", "```"},
		{"four backticks", "````diff", "````"},
		{"tildes", "~~~ python", "~~~"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inner := ""
			if c.close == "````" {
				inner = "```\nnested, not a close\n```\n"
			}
			report := "Here is the change:\n" + c.open + "\n" + inner + body + c.close + "\nDone."
			parts := splitReport(report, 400)
			if len(parts) < 2 {
				t.Fatalf("%d parts", len(parts))
			}
			for i, p := range parts {
				if len(p) > 400 {
					t.Errorf("part %d is %d bytes", i+1, len(p))
				}
				body := partMarker.ReplaceAllString(p, "")
				if fence, _ := scanFences(body, "", ""); fence != "" {
					t.Errorf("part %d leaves a %q block open:\n%s", i+1, fence, p)
				}
				if i > 0 && reopened(parts[i-1]) != "" && !strings.HasPrefix(p, c.open+"\n") {
					t.Errorf("part %d reopens with %q, want %q", i+1, strings.SplitN(p, "\n", 2)[0], c.open)
				}
			}
			if got := unsplit(t, parts); got != report {
				t.Fatalf("the parts rejoin to\n%q\nwant\n%q", got, report)
			}
		})
	}
	long := strings.Repeat("é", 1000) // no line breaks, two bytes a rune
	parts := splitReport(long, 301)
	for i, p := range parts {
		body := partMarker.ReplaceAllString(p, "")
		if len(p) > 301 || !utf8.ValidString(body) || !strings.HasSuffix(body, "é") {
			t.Errorf("part %d: %d bytes, ends %q", i+1, len(p), body[len(body)-2:])
		}
	}
	if got := unsplit(t, parts); got != long {
		t.Fatalf("the runes rejoin to %d bytes, want %d", len(got), len(long))
	}
}

// The figures about the turn ride on the last part only, every part keeps
// what identifies it as this agent's report, and every part carries the
// status line as a single report post does.
func TestReportPartsCarryTheTurnFiguresOnTheLastPartOnly(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	report := strings.Repeat(line, 3000) + "END"
	_, fp := relayReportSession(t, report, 20000)
	waitFor(t, "the report's parts", func() bool { return len(relayReports(fp)) == 3 })
	for i, p := range relayReports(fp) {
		meta, _ := p["meta"].(map[string]any)
		for _, k := range []string{"eagent", "kind", "relay", "agent", "seq", "source_anchor"} {
			if meta[k] == nil {
				t.Errorf("part %d has no %s", i+1, k)
			}
		}
		last := i == 2
		for _, k := range []string{"duration_ms", "tools", "interrupted", "images"} {
			if _, has := meta[k]; has != last {
				t.Errorf("part %d: %s present %v", i+1, k, has)
			}
		}
		if p["activity"] == nil {
			t.Errorf("part %d carries no status line, so it clears the thread's", i+1)
		}
	}
}

// keyPhone is a phone whose server has not been asked what it supports
// yet: its posts are queued and recorded, not sent.
func keyPhone(t *testing.T) (*phone, *[]finalechat.PostRequest) {
	t.Helper()
	var mu sync.Mutex
	var got []finalechat.PostRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req finalechat.PostRequest
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"message":{"id":"m1"},"thread":{"id":"t1"}}`))
	}))
	t.Cleanup(srv.Close)
	client := &finalechat.Client{BaseURL: srv.URL, Token: "fc_test"}
	p := &phone{client: client, ref: "ext:eagent:x", sid: "x", features: map[string]bool{}, queue: make(chan func(context.Context), 64), posted: map[string]bool{}, ctx: context.Background()}
	return p, &got
}

// A report recorded before the phone connection knows what the server
// supports still goes out with its idempotency key: the key is made when
// the post is sent, as every other post's is.
func TestReportPostedBeforeTheServerIsKnownKeepsItsKey(t *testing.T) {
	p, got := keyPhone(t)
	r := &Runtime{st: state.New(), relayNotifyDone: map[string]bool{}}
	ev := event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "codex", Text: "Fixed it."})
	ev.Seq = 7
	p.observeRelay(r, ev)
	// The first call learns the server's features; it runs first on the
	// worker, before the report.
	p.mu.Lock()
	p.features = featureSet([]string{"idempotency", "activity"})
	p.mu.Unlock()
	for len(p.queue) > 0 {
		(<-p.queue)(context.Background())
	}
	if len(*got) != 1 {
		t.Fatalf("%d posts", len(*got))
	}
	if (*got)[0].ClientKey == "" {
		t.Fatal("the report went out with no client key")
	}
}

var _ = time.Second
