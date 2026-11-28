package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// cacheRecorder is a fake Anthropic server that keeps every request body.
type cacheRecorder struct {
	mu     sync.Mutex
	bodies []string
	srv    *httptest.Server
}

func newCacheRecorder(t *testing.T) *cacheRecorder {
	rec := &cacheRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, string(raw))
		rec.mu.Unlock()
		anthropicSSE(w, "ok")
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

// cacheView describes one Anthropic request as the cache sees it: a key per
// content block for the prefix that ends with it (breakpoints left out, as
// they are not part of what is cached), and where the breakpoints are.
type cacheView struct {
	keys   []string
	points []int
}

func viewOf(t *testing.T, body string) cacheView {
	t.Helper()
	var req struct {
		System   json.RawMessage `json:"system"`
		Tools    []map[string]any
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	for _, tool := range req.Tools {
		delete(tool, "cache_control")
	}
	head, _ := json.Marshal(req.Tools)
	var b strings.Builder
	b.Write(head)
	b.WriteString(strings.ReplaceAll(string(req.System), `"cache_control":{"type":"ephemeral"},`, ""))
	var v cacheView
	for _, m := range req.Messages {
		b.WriteString("|" + m.Role + "|")
		for _, blk := range m.Content {
			if blk["cache_control"] != nil {
				v.points = append(v.points, len(v.keys))
			}
			delete(blk, "cache_control")
			raw, _ := json.Marshal(blk)
			b.Write(raw)
			v.keys = append(v.keys, b.String())
		}
	}
	return v
}

// checkBridges holds every request of a conversation to the rule the third
// breakpoint exists for: when the newest block an earlier request left a
// cache entry at is more than a breakpoint's look-back behind the end, a
// breakpoint sits on that very block; and any breakpoint before the last
// two is on a block an earlier request left an entry at.
func checkBridges(t *testing.T, bodies []string) {
	t.Helper()
	written := map[string]bool{}
	for n, body := range bodies {
		v := viewOf(t, body)
		if n > 0 {
			reach := -1
			for i := len(v.keys) - 1; i >= 0; i-- {
				if written[v.keys[i]] {
					reach = i
					break
				}
			}
			var bridge []int
			for _, p := range v.points {
				if p < len(v.keys)-2 {
					bridge = append(bridge, p)
				}
			}
			for _, p := range bridge {
				if !written[v.keys[p]] {
					t.Errorf("request %d: breakpoint on block %d of %d, which no earlier request left an entry at (the newest one is block %d)", n, p, len(v.keys), reach)
				}
			}
			if reach >= 0 && len(v.keys)-1-reach > anthropicLookback && (len(bridge) != 1 || bridge[0] != reach) {
				t.Errorf("request %d: the newest cached block is %d of %d, out of the end's reach, and the breakpoints are on %v", n, reach, len(v.keys), v.points)
			}
		}
		for _, p := range v.points {
			written[v.keys[p]] = true
		}
	}
}

func remarks(from, n int) []Message {
	var out []Message
	for i := from; i < from+n; i++ {
		out = append(out, Message{Role: "user", Text: fmt.Sprintf("[10:%02d:%02d] <Codex> step %d", i/60%60, i%60, i)})
	}
	return out
}

func joinMsgs(parts ...[]Message) []Message {
	var out []Message
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var r3Tools = []Tool{{Name: "hold", Parameters: json.RawMessage(`{"type":"object"}`)}, {Name: "send_message", Parameters: json.RawMessage(`{"type":"object"}`)}, {Name: "session_read", Parameters: json.RawMessage(`{"type":"object"}`)}}

// A reply sent back for another try puts blocks into the request that are
// never recorded. The call after the next recorded step must still find
// the entry where the record and that request agree, not a block counted
// from the end of the request that was sent back.
func TestBridgeAfterASendBackLandsOnABlockThePreviousRequestHad(t *testing.T) {
	rec := newCacheRecorder(t)
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: rec.srv.URL, Model: "claude", APIKey: "k"})
	base := joinMsgs([]Message{{Role: "user", Text: "[10:00:00] <user> merge the open PRs"}, {Role: "assistant", Text: "On it."}}, remarks(0, 6))
	tail := Message{Role: "user", Text: "[eagent 10:01:00] A look while the work goes on."}
	var sentBack []event.ToolCall
	var refusals []ToolResult
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("b%d", i)
		sentBack = append(sentBack, event.ToolCall{ID: id, Name: "relay_send", Args: json.RawMessage(`{}`)})
		refusals = append(refusals, ToolResult{CallID: id, Name: "relay_send", Output: "not accepted: say it to the user", IsError: true})
	}
	extra := []Message{{Role: "assistant", ToolCalls: sentBack}, {Role: "tool", Results: refusals}}
	reply := []Message{
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "s1", Name: "send_message", Args: json.RawMessage(`{"text":"hi"}`)}}},
		{Role: "tool", Results: []ToolResult{{CallID: "s1", Name: "send_message", Output: "sent"}}},
	}
	calls := [][]Message{
		joinMsgs(base, []Message{tail}),
		joinMsgs(base, []Message{tail}, extra),
		joinMsgs(base, []Message{tail}, reply, remarks(100, 25), []Message{{Role: "user", Text: "[eagent 10:09:00] A look."}}),
		joinMsgs(base, []Message{tail}, reply, remarks(100, 25), remarks(200, 12), []Message{{Role: "user", Text: "[eagent 10:12:00] A look."}}),
	}
	for i, msgs := range calls {
		if _, err := c.Complete(context.Background(), Request{System: "sys", Tools: r3Tools, Messages: msgs}, nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	checkBridges(t, rec.bodies)
}

// The first call of a fresh conversation (a dossier's, with no earlier
// one) has a single block. When the model answers with many lookups at
// once, the next request is more than a breakpoint's reach long, and the
// one block the first call cached must carry a breakpoint.
func TestBridgeOnAFreshConversationsFirstLongStep(t *testing.T) {
	rec := newCacheRecorder(t)
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: rec.srv.URL, Model: "claude", APIKey: "k"})
	prompt := Message{Role: "user", Text: "Write the dossier. The transcripts are listed below."}
	var lookups []event.ToolCall
	var found []ToolResult
	for i := 0; i < 11; i++ {
		id := fmt.Sprintf("l%d", i)
		lookups = append(lookups, event.ToolCall{ID: id, Name: "session_read", Args: json.RawMessage(fmt.Sprintf(`{"line":%d}`, i))})
		found = append(found, ToolResult{CallID: id, Name: "session_read", Output: fmt.Sprintf("line %d", i)})
	}
	for i, msgs := range [][]Message{
		{prompt},
		{prompt, {Role: "assistant", ToolCalls: lookups}, {Role: "tool", Results: found}},
	} {
		if _, err := c.Complete(context.Background(), Request{System: "sys", Tools: r3Tools, Messages: msgs}, nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	checkBridges(t, rec.bodies)
}

var writeGolden = flag.Bool("r3-golden", false, "rewrite testdata/r3_ordinary_session.jsonl from the current code")

// ordinarySession is a session's calls without send-backs: looks whose
// prompt end is not recorded, steps whose prompt end is, short steps and
// long ones, a picture, thinking replayed.
func ordinarySession(t *testing.T, img string) []Request {
	t.Helper()
	var record []Message
	record = append(record, Message{Role: "user", Text: "[10:00:00] <user> merge the open PRs", Images: []Image{{Path: img, MediaType: "image/png"}}})
	var reqs []Request
	n := 0
	steps := []struct {
		remarks int
		kept    bool
	}{{0, true}, {3, false}, {2, false}, {25, false}, {4, true}, {30, true}, {1, false}, {19, false}, {20, true}, {21, false}, {5, true}, {40, false}, {2, true}, {18, true}, {17, false}}
	for s, st := range steps {
		record = append(record, remarks(n, st.remarks)...)
		n += st.remarks
		tail := Message{Role: "user", Text: fmt.Sprintf("[eagent 11:%02d:00] A look while the work goes on.", s)}
		msgs := append(record[:len(record):len(record)], tail)
		reqs = append(reqs, Request{System: "sys", Tools: r3Tools, Messages: msgs})
		if st.kept {
			id := fmt.Sprintf("m%d", s)
			native, _ := json.Marshal([]map[string]any{
				{"type": "thinking", "thinking": "plan", "signature": "sig"},
				{"type": "tool_use", "id": id, "name": "send_message", "input": map[string]any{"text": "progress"}},
			})
			record = append(record, tail,
				Message{Role: "assistant", ToolCalls: []event.ToolCall{{ID: id, Name: "send_message", Args: json.RawMessage(`{"text":"progress"}`)}}, Native: native, NativeProtocol: ProtocolAnthropic},
				Message{Role: "tool", Results: []ToolResult{{CallID: id, Name: "send_message", Output: "sent"}}})
		}
	}
	return reqs
}

// An ordinary session's requests are the same bytes they were before the
// third breakpoint learned where the entries really are, except where it
// moved to a block nearer the end that the previous request also had (the
// previous prompt's end, when it was recorded) or was left out because
// that block is within the end's reach.
func TestOrdinarySessionRequestsAreUnchangedButForABetterBridge(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nfake picture"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := newCacheRecorder(t)
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: rec.srv.URL, Model: "claude", APIKey: "k", ReasoningEffort: "high"})
	for i, req := range ordinarySession(t, img) {
		if _, err := c.Complete(context.Background(), req, nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	golden := filepath.Join("testdata", "r3_ordinary_session.jsonl")
	if *writeGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(strings.Join(rec.bodies, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.Open(golden)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var before []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		before = append(before, sc.Text())
	}
	if len(before) != len(rec.bodies) {
		t.Fatalf("%d requests now, %d before", len(rec.bodies), len(before))
	}
	strip := func(s string) string { return strings.ReplaceAll(s, `"cache_control":{"type":"ephemeral"},`, "") }
	var moved []int
	for i := range before {
		now := rec.bodies[i]
		if now == before[i] {
			continue
		}
		if strip(now) != strip(before[i]) {
			t.Fatalf("request %d differs in more than its breakpoints:\nbefore %s\nnow    %s", i, before[i], now)
		}
		vb, vn := viewOf(t, before[i]), viewOf(t, now)
		last := len(vn.keys) - 2
		bridgeOf := func(v cacheView) int {
			for _, p := range v.points {
				if p < last {
					return p
				}
			}
			return -1
		}
		ob, nb := bridgeOf(vb), bridgeOf(vn)
		// The previous request's last block is in this one: that is where
		// the move goes, or the bridge is not needed at all.
		prev := viewOf(t, rec.bodies[i-1])
		end := prev.keys[len(prev.keys)-1]
		at := -1
		for k, key := range vn.keys {
			if key == end {
				at = k
			}
		}
		if at < 0 || ob < 0 || !(nb == at || (nb < 0 && len(vn.keys)-1-at <= anthropicLookback)) {
			t.Fatalf("request %d: the bridge was on block %d and is now on %d; the previous request's end is block %d", i, ob, nb, at)
		}
		moved = append(moved, i)
	}
	t.Logf("requests whose bridge moved: %v of %d", moved, len(before))
	checkBridges(t, rec.bodies)
}
