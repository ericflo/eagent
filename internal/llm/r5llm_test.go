package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// With reasoning replay off, a turn whose reasoning items are dropped must
// not go back with the ids of the items that came with them: OpenAI refuses
// a function_call or message sent without the reasoning item it was made
// with. Such a turn is rebuilt from its text and calls. A turn that had no
// reasoning goes back as it came.
func TestResponsesWithoutReasoningReplayRebuildsATurnThatHadReasoning(t *testing.T) {
	withReasoning := json.RawMessage(`[{"type":"reasoning","id":"rs_1","summary":[]},{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"checking"}]},{"type":"function_call","id":"fc_1","call_id":"c1","name":"ping","arguments":"{}"}]`)
	plain := json.RawMessage(`[{"type":"function_call","id":"fc_2","call_id":"c2","name":"ping","arguments":"{}"}]`)
	req := Request{Messages: []Message{
		{Role: "user", Text: "go"},
		{Role: "assistant", Text: "checking", ToolCalls: []event.ToolCall{{ID: "c1", Name: "ping", Args: json.RawMessage(`{}`)}}, Native: withReasoning, NativeProtocol: ProtocolResponses},
		{Role: "tool", Results: []ToolResult{{CallID: "c1", Name: "ping", Output: "pong"}}},
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "c2", Name: "ping", Args: json.RawMessage(`{}`)}}, Native: plain, NativeProtocol: ProtocolResponses},
		{Role: "tool", Results: []ToolResult{{CallID: "c2", Name: "ping", Output: "pong"}}},
	}}
	raw, _ := json.Marshal(responsesInput(req, false, ""))
	body := string(raw)
	for _, id := range []string{"rs_1", "msg_1", "fc_1"} {
		if strings.Contains(body, id) {
			t.Fatalf("%s went back without its reasoning: %s", id, body)
		}
	}
	if !strings.Contains(body, `"call_id":"c1"`) || !strings.Contains(body, `"content":"checking"`) {
		t.Fatalf("the turn was not rebuilt from its text and calls: %s", body)
	}
	if !strings.Contains(body, `"id":"fc_2"`) {
		t.Fatalf("a turn without reasoning should replay as it came: %s", body)
	}
}

// anthropicConversation is a conversation of n remarks after an opening.
func anthropicConversation(n int) []Message {
	msgs := []Message{{Role: "user", Text: "[10:00:00] <user> start"}, {Role: "assistant", Text: "On it."}}
	for i := 0; i < n; i++ {
		msgs = append(msgs, Message{Role: "user", Text: fmt.Sprintf("[10:01:%02d] <Codex> step %d", i, i)})
	}
	return msgs
}

func breakpointsIn(t *testing.T, body string) int {
	t.Helper()
	var req struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b["cache_control"] != nil {
				n++
			}
		}
	}
	return n
}

// A stream that fails before the provider said it started (no
// message_start) wrote no cache entry. Its breakpoints must not be taken
// for entries, or the retry of the same request drops the bridge to the
// entry that does exist and writes the whole conversation again.
func TestAnthropicStreamThatFailsBeforeItStartsLeavesNoCacheEntry(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		n := len(bodies)
		mu.Unlock()
		if n == 2 {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
			return
		}
		anthropicSSE(w, "ok")
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k"})
	c.MaxAttempts = 1
	if _, err := c.Complete(context.Background(), Request{System: "s", Messages: anthropicConversation(2)}, nil); err != nil {
		t.Fatal(err)
	}
	long := Request{System: "s", Messages: anthropicConversation(40)}
	if _, err := c.Complete(context.Background(), long, nil); err == nil {
		t.Fatal("the overloaded stream should fail")
	}
	if _, err := c.Complete(context.Background(), long, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if a, b := breakpointsIn(t, bodies[1]), breakpointsIn(t, bodies[2]); a != 3 || b != 3 {
		t.Fatalf("breakpoints in the failed request and its retry = %d, %d; want the bridge in both (3)", a, b)
	}
}

// When the other thinking flavour gets past the flavour check and the call
// then fails for another reason, that reason is what the caller must act
// on (here a conversation too long, which a restart fixes), not the stale
// refusal of the first flavour.
func TestAnthropicFlavourRetryReportsTheErrorThatStoppedIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.WriteHeader(400)
		if strings.Contains(string(raw), `"adaptive"`) {
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"thinking.type: Input tag 'adaptive' found using 'type' does not match any of the expected tags"}}`)
			return
		}
		fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000 maximum"}}`)
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k", ReasoningEffort: "high", MaxTokens: 32768})
	c.MaxAttempts = 1
	_, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	var ae *APIError
	if !errors.As(err, &ae) || !ae.ContextOverflow() {
		t.Fatalf("err = %v; want the overflow", err)
	}
	if !c.anthropicBudgeted.Load() {
		t.Fatal("the flavour that got past the flavour check was not kept")
	}
}

// OpenAI refuses one bad picture with a code of its own. That is about the
// picture: the model can still see, and the next picture is sent.
func TestOpenAIPerImageCodesAreAboutThePictureNotTheModel(t *testing.T) {
	for _, code := range []string{"invalid_image", "image_too_large", "image_parse_error", "invalid_image_url"} {
		e := &APIError{Status: 400, Code: code, Body: `{"error":{"message":"Invalid image.","type":"invalid_request_error","param":null,"code":"` + code + `"}}`}
		if !e.RejectsImages() || !e.badImage() {
			t.Fatalf("%s: RejectsImages=%v badImage=%v", code, e.RejectsImages(), e.badImage())
		}
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.png")
	good := filepath.Join(dir, "good.png")
	for p, b := range map[string]string{bad: "\x89PNG\r\n\x1a\nbroken", good: "\x89PNG\r\n\x1a\nfine!!"} {
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	badData := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nbroken"))
	goodData := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfine!!"))
	sawGood := false
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), badData) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"The image data you provided does not represent a valid image.","type":"invalid_request_error","param":null,"code":"image_parse_error"}}`)
			return
		}
		if strings.Contains(string(raw), goodData) {
			sawGood = true
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "gpt-x", APIKey: "k"})
	c.MaxAttempts = 1
	first := []Message{{Role: "user", Text: "look", Images: []Image{{Path: bad, MediaType: "image/png"}}}}
	if _, err := c.Complete(context.Background(), Request{Messages: first}, nil); err != nil {
		t.Fatal(err)
	}
	if c.rejectsImages.Load() {
		t.Fatal("one bad picture marked the model as unable to see")
	}
	second := append(first, Message{Role: "assistant", Text: "ok"}, Message{Role: "user", Text: "and this", Images: []Image{{Path: good, MediaType: "image/png"}}})
	if _, err := c.Complete(context.Background(), Request{Messages: second}, nil); err != nil {
		t.Fatal(err)
	}
	if !sawGood {
		t.Fatal("the next picture was not sent")
	}
}

// A stream whose last line has no newline before the connection closes
// still ends with that line.
func TestSSEFinalDataLineWithoutANewlineIsRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"type":"message_start","message":{"model":"claude-test","usage":{"input_tokens":5}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		fmt.Fprint(w, `data: {"type":"message_stop"}`)
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k"})
	c.MaxAttempts = 1
	resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	if err != nil || resp.Text != "done" {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
}

// lateTimer is an idle timer that has fired (Stop reports false) but whose
// function has not run yet when the response arrives: it runs a moment
// later, cancelling the request.
type lateTimer struct{ f func() }

func (l lateTimer) Stop() bool {
	go func() { time.Sleep(20 * time.Millisecond); l.f() }()
	return false
}

// The idle limit running out just as the response arrives is an idle
// timeout, which a retry can fix, not a cancelled call.
func TestIdleLimitRunningOutAsTheResponseArrivesIsAnIdleTimeout(t *testing.T) {
	old := idleAfterFunc
	idleAfterFunc = func(_ time.Duration, f func()) interface{ Stop() bool } { return lateTimer{f} }
	defer func() { idleAfterFunc = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.IdleTimeout = 5 * time.Second
	c.MaxAttempts = 1
	_, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil)
	if !errors.Is(err, ErrIdle) || !retryable(err) {
		t.Fatalf("err = %v; want a retryable idle timeout", err)
	}
}

// llama-server says a prompt is over its window in words of its own.
func TestLlamaServerOverflowIsAContextOverflow(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":400,"message":"request (40000 tokens) exceeds the available context size (32768 tokens), try increasing it","type":"exceed_context_size_error","n_prompt_tokens":40000,"n_ctx":32768}}`,
		`{"error":{"code":400,"message":"the request exceeds the available context size. try increasing the context size or enable context shift","type":"invalid_request_error"}}`,
	} {
		if !(&APIError{Status: 400, Body: body}).ContextOverflow() {
			t.Fatalf("not an overflow: %s", body)
		}
	}
}

// With budgeted thinking, Anthropic refuses a request whose last assistant
// turn in a tool loop has no thinking block (a turn another model wrote,
// rebuilt from its text and calls). The call goes again without thinking
// rather than failing, and the flavour stays as it was.
func TestBudgetedThinkingAfterARebuiltToolTurnGoesAgainWithoutThinking(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		if strings.Contains(string(raw), `"thinking"`) {
			w.WriteHeader(400)
			fmt.Fprint(w, "{\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"messages.1.content.0.type: Expected `thinking` or `redacted_thinking`, but found `tool_use`. When `thinking` is enabled, a final `assistant` message must start with a thinking block (preceeding the lastmost set of `tool_use` and `tool_result` blocks). We recommend you include thinking blocks from previous turns. To avoid this requirement, disable `thinking`. Please consult our documentation at https://docs.claude.com/en/docs/build-with-claude/extended-thinking\"}}")
			return
		}
		anthropicSSE(w, "ok")
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude-x", APIKey: "k", ReasoningEffort: "high", MaxTokens: 32768})
	c.anthropicBudgeted.Store(true)
	c.MaxAttempts = 1
	req := Request{Messages: []Message{
		{Role: "user", Text: "go"},
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "t1", Name: "ping", Args: json.RawMessage(`{}`)}}},
		{Role: "tool", Results: []ToolResult{{CallID: "t1", Name: "ping", Output: "pong"}}},
	}}
	if _, err := c.Complete(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || strings.Contains(bodies[1], `"thinking"`) {
		t.Fatalf("posts = %d; the second must carry no thinking", len(bodies))
	}
	if !c.anthropicBudgeted.Load() {
		t.Fatal("the flavour changed")
	}
}
