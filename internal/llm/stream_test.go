package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

func sseServer(t *testing.T, handler func(w http.ResponseWriter, body map[string]any)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		handler(w, body)
	}))
}

func TestChatStreamingToolCalls(t *testing.T) {
	var gotBody map[string]any
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		gotBody = body
		chunks := []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":80},"completion_tokens_details":{"reasoning_tokens":5}}}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k", ReasoningEffort: "low"})
	var toolNames []string
	resp, err := c.Complete(context.Background(), Request{
		System:   "sys",
		Messages: []Message{{Role: "user", Text: "hi"}},
		Tools:    []Tool{{Name: "bash", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}, &Observer{ToolCall: func(n string) { toolNames = append(toolNames, n) }})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Hello" || resp.Reasoning != "think" {
		t.Fatalf("text=%q reasoning=%q", resp.Text, resp.Reasoning)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].ID != "call_1" || string(resp.ToolCalls[0].Args) != `{"command":"ls"}` || resp.ToolCalls[1].Name != "read_file" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.Usage.Input != 100 || resp.Usage.Cached != 80 || resp.Usage.Reasoning != 5 || resp.Stop != "tool_calls" {
		t.Fatalf("usage=%+v stop=%s", resp.Usage, resp.Stop)
	}
	if gotBody["reasoning_effort"] != "low" || gotBody["stream"] != true {
		t.Fatalf("request body = %v", gotBody)
	}
	if len(toolNames) != 2 {
		t.Fatalf("observer saw %v", toolNames)
	}
}

func TestChatLengthStopAndTextFallback(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"<tool_call>bash\n<arg_key>command</arg_key>\n<arg_value>pwd</arg_value>\n</tool_call>"},"finish_reason":"stop"}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "bash" || resp.Stop != "tool_calls" {
		t.Fatalf("fallback parse failed: %+v stop=%s", resp.ToolCalls, resp.Stop)
	}
}

func TestRetryOn503ThenSuccess(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"message":"no available server"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	retried := false
	c.OnRetry = func(int, error, time.Duration) { retried = true }
	// Shrink backoff for the test by using a tiny attempt count path.
	go func() {}()
	start := time.Now()
	resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" || !retried || atomic.LoadInt32(&n) != 2 {
		t.Fatalf("text=%q retried=%v n=%d", resp.Text, retried, n)
	}
	if time.Since(start) < time.Second {
		t.Fatal("expected backoff before retry")
	}
}

func TestQuotaErrorNotRetried(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"message":"You have no credits remaining.","code":"credit_balance_exhausted"}}`)
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	_, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil)
	if err == nil || atomic.LoadInt32(&n) != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestIdleTimeoutAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"partial"}}]}`)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // hang until the client gives up
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.IdleTimeout = 300 * time.Millisecond
	c.MaxAttempts = 1
	start := time.Now()
	_, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "idle") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("idle timeout took too long")
	}
}

// A server that takes the request and never answers (the network dropped
// under the connection) is given up on after the idle limit, not the whole
// call's 15 minutes, and the failure is one a retry can fix.
func TestIdleTimeoutCoversTheWaitForAResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // so the server notices the client leave
		<-r.Context().Done()               // never send headers
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.IdleTimeout = 300 * time.Millisecond
	c.MaxAttempts = 1
	start := time.Now()
	_, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil)
	if !errors.Is(err, ErrIdle) || !retryable(err) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("waited past the idle limit for a response")
	}
}

// A call that keeps running out its whole limit (a gateway that keeps the
// connection alive without answering) is tried twice, not six times.
func TestCallTimeoutIsRetriedOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		for {
			if _, err := fmt.Fprint(w, ": PROCESSING\n\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.CallTimeout = 300 * time.Millisecond
	c.MaxAttempts = 6
	_, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil)
	if err == nil || calls.Load() != 2 {
		t.Fatalf("err=%v calls=%d, want 2 calls", err, calls.Load())
	}
}

func TestResponsesStreaming(t *testing.T) {
	var gotBody map[string]any
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		gotBody = body
		events := []string{
			`{"type":"response.output_item.added","item":{"type":"reasoning","id":"rs_1"}}`,
			`{"type":"response.reasoning_summary_text.delta","delta":"plan"}`,
			`{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","status":"completed","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"ENC"}}`,
			`{"type":"response.output_text.delta","delta":"Hi"}`,
			`{"type":"response.output_item.done","item":{"type":"message","id":"m1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}}`,
			`{"type":"response.output_item.added","item":{"type":"function_call","name":"bash"}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc1","status":"completed","call_id":"call_9","name":"bash","arguments":"{\"command\":\"ls\"}"}}`,
			`{"type":"response.completed","response":{"model":"gpt-x","status":"completed","output":[{"type":"reasoning","id":"rs_1","status":"completed","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"ENC"},{"type":"message","id":"m1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hi"}]},{"type":"function_call","id":"fc1","status":"completed","call_id":"call_9","name":"bash","arguments":"{\"command\":\"ls\"}"}],"usage":{"input_tokens":50,"output_tokens":9,"input_tokens_details":{"cached_tokens":40},"output_tokens_details":{"reasoning_tokens":3}}}}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
		}
	})
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolResponses, BaseURL: srv.URL, Model: "gpt-x", APIKey: "k", ReasoningEffort: "medium"})
	resp, err := c.Complete(context.Background(), Request{
		System: "sys", CacheKey: "ck",
		Messages: []Message{{Role: "user", Text: "hi"}},
		Tools:    []Tool{{Name: "bash", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Hi" || resp.Reasoning != "plan" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_9" {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Usage.Input != 50 || resp.Usage.Cached != 40 || resp.Model != "gpt-x" {
		t.Fatalf("usage = %+v model=%s", resp.Usage, resp.Model)
	}
	// Native must carry the reasoning item with encrypted content, sans status.
	var native []map[string]any
	if err := json.Unmarshal(resp.Native, &native); err != nil || len(native) != 3 {
		t.Fatalf("native = %s", resp.Native)
	}
	if native[0]["encrypted_content"] != "ENC" {
		t.Fatal("encrypted reasoning not preserved")
	}
	if _, has := native[0]["status"]; has {
		t.Fatal("status should be stripped for replay")
	}
	if gotBody["prompt_cache_key"] != "ck" || gotBody["store"] != false || gotBody["instructions"] != "sys" {
		t.Fatalf("body = %v", gotBody)
	}
	// Replaying the turn sends the native items verbatim.
	items := responsesInput(Request{Messages: []Message{
		{Role: "user", Text: "hi"},
		{Role: "assistant", Native: resp.Native, NativeProtocol: ProtocolResponses, Text: "Hi", ToolCalls: resp.ToolCalls},
		{Role: "tool", Results: []ToolResult{{CallID: "call_9", Output: "a b"}}},
	}}, true, "")
	if len(items) != 5 {
		t.Fatalf("replayed %d items: %v", len(items), items)
	}
	if m, ok := items[4].(map[string]any); !ok || m["type"] != "function_call_output" {
		t.Fatalf("last item = %v", items[4])
	}
}

func TestAnthropicStreamingAndAlternation(t *testing.T) {
	var gotBody map[string]any
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		gotBody = body
		events := []string{
			`{"type":"message_start","message":{"model":"claude","usage":{"input_tokens":10,"cache_read_input_tokens":30,"cache_creation_input_tokens":5}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Sure."}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"bash","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":" \"ls\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`,
			`{"type":"message_stop"}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", e)
		}
	})
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude", APIKey: "k"})
	resp, err := c.Complete(context.Background(), Request{
		System: "sys",
		Messages: []Message{
			{Role: "user", Text: "a"},
			{Role: "assistant", Text: "b", ToolCalls: []event.ToolCall{{ID: "t0", Name: "bash", Args: json.RawMessage(`{"command":"x"}`)}}},
			{Role: "tool", Results: []ToolResult{{CallID: "t0", Output: "out"}}},
			{Role: "user", Text: "c"}, // must merge with the tool results into one user turn
		},
		Tools: []Tool{{Name: "bash", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Sure." || len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Args) != `{"command": "ls"}` || resp.Stop != "tool_calls" {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Usage.Input != 45 || resp.Usage.Cached != 30 || resp.Usage.Output != 12 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	msgs := gotBody["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected strict alternation (3 messages), got %d: %v", len(msgs), msgs)
	}
	last := msgs[2].(map[string]any)
	if last["role"] != "user" || len(last["content"].([]any)) != 2 {
		t.Fatalf("tool results and user text not merged: %v", last)
	}
	sys := gotBody["system"].([]any)[0].(map[string]any)
	if sys["cache_control"] == nil {
		t.Fatal("system prompt should carry a cache breakpoint")
	}
}

func TestTruncatedStreamIsRetried(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if atomic.AddInt32(&n, 1) == 1 {
			// Connection drops mid tool call: no finish_reason, no usage.
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"delegate","arguments":"{"}}]}}]}`)
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" || atomic.LoadInt32(&n) != 2 {
		t.Fatalf("text=%q attempts=%d", resp.Text, n)
	}
}

func TestMalformedArgsAreSanitisedOnReplay(t *testing.T) {
	msgs, _ := chatMessages(Request{Messages: []Message{
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "c1", Name: "delegate", Args: normalizeArgs("{")}}},
		{Role: "tool", Results: []ToolResult{{CallID: "c1", Output: "rejected"}}},
	}}, true, "")
	fn := msgs[0]["tool_calls"].([]map[string]any)[0]["function"].(map[string]any)
	if !json.Valid([]byte(fn["arguments"].(string))) {
		t.Fatalf("replayed arguments are not valid JSON: %s", fn["arguments"])
	}
	items := responsesInput(Request{Messages: []Message{
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "c1", Name: "delegate", Args: normalizeArgs("{")}}},
	}}, false, "")
	if it := items[0].(map[string]any); !json.Valid([]byte(it["arguments"].(string))) {
		t.Fatalf("responses replay arguments invalid: %s", it["arguments"])
	}
}

func TestResponsesDanglingReasoningDropped(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		events := []string{
			`{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","status":"completed","summary":[],"encrypted_content":"ENC"}}`,
			`{"type":"response.incomplete","response":{"model":"gpt-x","status":"incomplete","output":[{"type":"reasoning","id":"rs_1","status":"completed","summary":[],"encrypted_content":"ENC"}],"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":5,"output_tokens":100}}}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
		}
	})
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolResponses, BaseURL: srv.URL, Model: "gpt-x", APIKey: "k"})
	resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Stop != "length" || len(resp.Native) != 0 {
		t.Fatalf("stop=%s native=%s; a lone reasoning item must not be kept", resp.Stop, resp.Native)
	}
	// Even if an old log carries one, replay drops it.
	items := responsesInput(Request{Messages: []Message{
		{Role: "user", Text: "hi"},
		{Role: "assistant", Native: json.RawMessage(`[{"type":"reasoning","id":"rs_1","encrypted_content":"ENC"}]`), NativeProtocol: ProtocolResponses, Text: ""},
		{Role: "user", Text: "continue"},
	}}, true, "")
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && m["type"] == "reasoning" {
			t.Fatal("dangling reasoning item replayed")
		}
	}
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
}

func TestSyntheticIDsAreUniqueAcrossResponses(t *testing.T) {
	a, _ := ParseTextToolCalls("<tool_call>bash\n<arg_key>command</arg_key><arg_value>ls</arg_value></tool_call>")
	time.Sleep(2 * time.Millisecond)
	b, _ := ParseTextToolCalls("<tool_call>bash\n<arg_key>command</arg_key><arg_value>ls</arg_value></tool_call>")
	if len(a) != 1 || len(b) != 1 || a[0].ID == b[0].ID {
		t.Fatalf("ids should differ: %v %v", a, b)
	}
}

func TestTextToolCallMentionIsNotACall(t *testing.T) {
	calls, rest := ParseTextToolCalls("Some models emit <tool_call> markup as text, which we recover.")
	if len(calls) != 0 || !strings.Contains(rest, "<tool_call>") {
		t.Fatalf("prose mentioning the tag was treated as a call: %v %q", calls, rest)
	}
}

func TestNonStreamingJSONFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"m","choices":[{"message":{"content":null,"tool_calls":[{"id":"x1","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
	}))
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "x"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "bash" || resp.Usage.Input != 3 {
		t.Fatalf("resp = %+v", resp)
	}
}

// chatSSE serves one streamed chat completion made of the given chunks.
func chatSSE(chunks ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// Deltas without an index continue the last call; a new call is only
// opened when a delta announces one.
func TestChatToolCallDeltasWithoutIndex(t *testing.T) {
	srv := chatSSE(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"{\"command\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"\"ls\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.MaxAttempts = 1
	resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "bash" {
		t.Fatalf("calls = %+v", resp.ToolCalls)
	}
	if args, err := ArgsObject(resp.ToolCalls[0].Args); err != nil || args["command"] != "ls" {
		t.Fatalf("args = %s (%v)", resp.ToolCalls[0].Args, err)
	}
	// Two complete calls announced by name, still without an index, stay separate.
	srv2 := chatSSE(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"id":"a","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"id":"b","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"x\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	defer srv2.Close()
	c2 := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv2.URL, Model: "m", APIKey: "k"})
	c2.MaxAttempts = 1
	resp, err = c2.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	if err != nil || len(resp.ToolCalls) != 2 || resp.ToolCalls[0].Name != "bash" || resp.ToolCalls[1].Name != "read_file" {
		t.Fatalf("two index-less calls: %+v %v", resp.ToolCalls, err)
	}
}

// A stored function_call whose arguments were cut off is sanitised on
// replay, and native items replay only at the host that minted them.
func TestNativeReplayIsSanitisedAndHostBound(t *testing.T) {
	native, _ := json.Marshal([]map[string]any{
		{"type": "reasoning", "id": "rs_1", "summary": []any{}, "encrypted_content": "ENC"},
		{"type": "function_call", "id": "fc_1", "call_id": "c1", "name": "bash", "arguments": `{"command": "ls`},
	})
	msg := Message{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "c1", Name: "bash", Args: normalizeArgs(`{"command": "ls`)}}, Native: native, NativeProtocol: ProtocolResponses, NativeHost: "https://api.openai.com/v1"}
	items := responsesInput(Request{Messages: []Message{msg}}, true, "https://api.openai.com/v1")
	if len(items) != 2 {
		t.Fatalf("same host should replay both native items: %v", items)
	}
	fc := items[1].(json.RawMessage)
	var it map[string]any
	_ = json.Unmarshal(fc, &it)
	if args, _ := it["arguments"].(string); !json.Valid([]byte(args)) || !strings.Contains(args, "_malformed") {
		t.Fatalf("replayed native arguments not sanitised: %s", fc)
	}
	// Another host (a fallback route): the turn is rebuilt from text and
	// tool calls, and the encrypted reasoning stays home.
	items = responsesInput(Request{Messages: []Message{msg}}, true, "https://openrouter.ai/api/v1")
	raw, _ := json.Marshal(items)
	if strings.Contains(string(raw), "ENC") || strings.Contains(string(raw), "rs_1") {
		t.Fatalf("native items replayed at a different host: %s", raw)
	}
	if len(items) != 1 || items[0].(map[string]any)["type"] != "function_call" {
		t.Fatalf("rebuilt turn = %s", raw)
	}
	// Logs from before the host was recorded keep replaying as before.
	msg.NativeHost = ""
	if items = responsesInput(Request{Messages: []Message{msg}}, true, "https://openrouter.ai/api/v1"); len(items) != 2 {
		t.Fatalf("an unrecorded host must not stop replay: %v", items)
	}
}

func TestKeylessEndpointsOmitAuthHeaders(t *testing.T) {
	t.Run("chat", func(t *testing.T) {
		var auth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"model":"local-m","choices":[{"message":{"content":"ok","reasoning_content":"step"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":1}}}`)
		}))
		defer srv.Close()
		c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "local-m"})
		resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			t.Fatalf("expected no Authorization header, got %q", auth)
		}
		if resp.Text != "ok" || resp.Reasoning != "step" || resp.Usage.Cached != 2 || resp.Usage.Reasoning != 1 {
			t.Fatalf("resp = %+v", resp)
		}
	})

	t.Run("responses", func(t *testing.T) {
		var auth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"model":"local-r","status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[],"content":[{"type":"reasoning_text","text":"deep thought"}]},{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"done"}]},{"type":"function_call","id":"fc1","call_id":"call_1","name":"ping","arguments":"{\"message\":\"pong\"}"}],"usage":{"input_tokens":12,"output_tokens":7,"output_tokens_details":{"reasoning_tokens":4}}}`)
		}))
		defer srv.Close()
		c := NewClient(Endpoint{Protocol: ProtocolResponses, BaseURL: srv.URL, Model: "local-r"})
		resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			t.Fatalf("expected no Authorization header, got %q", auth)
		}
		if resp.Text != "done" || resp.Reasoning != "deep thought" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "ping" || resp.Usage.Reasoning != 4 {
			t.Fatalf("resp = %+v", resp)
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		var key, ver string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key = r.Header.Get("x-api-key")
			ver = r.Header.Get("anthropic-version")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"model":"local-a","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"calling"},{"type":"tool_use","id":"toolu_1","name":"ping","input":{"message":"pong"}}],"usage":{"input_tokens":10,"output_tokens":6,"cache_read_input_tokens":3}}`)
		}))
		defer srv.Close()
		c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "local-a"})
		resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			t.Fatalf("expected no x-api-key header, got %q", key)
		}
		if ver != "2023-06-01" {
			t.Fatalf("anthropic-version = %q", ver)
		}
		if resp.Text != "calling" || resp.Reasoning != "hmm" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "toolu_1" || resp.Usage.Cached != 3 {
			t.Fatalf("resp = %+v", resp)
		}
	})
}

func TestResponsesReasoningTextInContentAndAnthropicInlineToolInput(t *testing.T) {
	t.Run("responses reasoning_text", func(t *testing.T) {
		srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
			events := []string{
				`{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"plan A"}]}}`,
				`{"type":"response.done","response":{"model":"local-r","status":"completed","output":[{"type":"reasoning","id":"rs_1","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"plan A"}]},{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":10,"output_tokens":5}}}`,
			}
			for _, e := range events {
				fmt.Fprintf(w, "data: %s\n\n", e)
			}
		})
		defer srv.Close()
		c := NewClient(Endpoint{Protocol: ProtocolResponses, BaseURL: srv.URL, Model: "local-r"})
		resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Reasoning != "plan A" || resp.Text != "answer" {
			t.Fatalf("resp = %+v", resp)
		}
	})

	t.Run("anthropic inline tool_use input", func(t *testing.T) {
		srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
			events := []string{
				`event: message_start`,
				`data: {"type":"message_start","message":{"model":"local-a","usage":{"input_tokens":8}}}`,
				``,
				`event: content_block_start`,
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"ping","input":{"message":"pong"}}}`,
				``,
				`event: content_block_stop`,
				`data: {"type":"content_block_stop","index":0}`,
				``,
				`event: message_delta`,
				`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
				``,
				`event: message_stop`,
				`data: {"type":"message_stop"}`,
				``,
			}
			fmt.Fprint(w, strings.Join(events, "\n"))
		})
		defer srv.Close()
		c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "local-a"})
		resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "ping" {
			t.Fatalf("tool calls = %+v", resp.ToolCalls)
		}
		args, err := ArgsObject(resp.ToolCalls[0].Args)
		if err != nil || args["message"] != "pong" {
			t.Fatalf("args = %s err=%v", resp.ToolCalls[0].Args, err)
		}
	})
}

func TestChatReasoningReplayPromptCacheKeyAndFallbacks(t *testing.T) {
	longThink := strings.Repeat("step ", 1000) // > 4000 chars, must stay untruncated in Native

	t.Run("streaming capture and same-host replay", func(t *testing.T) {
		var bodies []map[string]any
		srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
			bodies = append(bodies, body)
			part1, _ := json.Marshal(longThink[:2500])
			part2, _ := json.Marshal(longThink[2500:])
			chunks := []string{
				`{"model":"local-r","choices":[{"index":0,"delta":{"reasoning_content":` + string(part1) + `}}]}`,
				`{"choices":[{"index":0,"delta":{"reasoning_content":` + string(part2) + `,"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]}}]}`,
				`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":10}}`,
			}
			for _, ch := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", ch)
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		})
		defer srv.Close()

		c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "local-r", ReplayReasoning: true})
		resp, err := c.Complete(context.Background(), Request{
			CacheKey: "sess-1-orch",
			Messages: []Message{{Role: "user", Text: "list files"}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Reasoning != longThink {
			t.Fatalf("reasoning len=%d want %d", len(resp.Reasoning), len(longThink))
		}
		var nat chatNative
		if err := json.Unmarshal(resp.Native, &nat); err != nil || nat.ReasoningContent != longThink || nat.Reasoning != "" {
			t.Fatalf("native = %s err=%v", resp.Native, err)
		}
		if bodies[0]["prompt_cache_key"] != "sess-1-orch" {
			t.Fatalf("prompt_cache_key = %v", bodies[0]["prompt_cache_key"])
		}

		// Same host + replay enabled => reasoning_content replayed verbatim.
		req2 := Request{Messages: []Message{
			{Role: "user", Text: "list files"},
			{Role: "assistant", ToolCalls: resp.ToolCalls, Native: resp.Native, NativeProtocol: ProtocolChat, NativeHost: srv.URL},
			{Role: "tool", Results: []ToolResult{{CallID: "c1", Output: "a.go"}}},
		}}
		msgs, replayed := chatMessages(req2, true, srv.URL)
		if !replayed || msgs[1]["reasoning_content"] != longThink {
			t.Fatalf("expected reasoning_content replay: replayed=%v got=%v", replayed, msgs[1]["reasoning_content"])
		}
		// Disabled => omitted.
		msgsNo, replayedNo := chatMessages(req2, false, srv.URL)
		if replayedNo || msgsNo[1]["reasoning_content"] != nil {
			t.Fatalf("expected no replay when disabled: replayed=%v msg=%v", replayedNo, msgsNo[1])
		}
		// Different host => omitted.
		msgsHost, replayedHost := chatMessages(req2, true, "http://other.local:1234/v1")
		if replayedHost || msgsHost[1]["reasoning_content"] != nil {
			t.Fatalf("expected no replay on different host: replayed=%v msg=%v", replayedHost, msgsHost[1])
		}
	})

	t.Run("non-streaming capture with reasoning field", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"model":"harmony","choices":[{"message":{"role":"assistant","content":"hi","reasoning":"thought chain"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
		}))
		defer srv.Close()
		c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "harmony", ReplayReasoning: true})
		resp, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var nat chatNative
		if err := json.Unmarshal(resp.Native, &nat); err != nil || nat.Reasoning != "thought chain" || nat.ReasoningContent != "" {
			t.Fatalf("native = %s err=%v", resp.Native, err)
		}
		msgs, replayed := chatMessages(Request{Messages: []Message{
			{Role: "user", Text: "hi"},
			{Role: "assistant", Text: "hi", Native: resp.Native, NativeProtocol: ProtocolChat, NativeHost: srv.URL},
			{Role: "user", Text: "next"},
		}}, true, srv.URL)
		if !replayed || msgs[1]["reasoning"] != "thought chain" || msgs[1]["reasoning_content"] != nil {
			t.Fatalf("replayed=%v msg=%v", replayed, msgs[1])
		}
	})

	t.Run("strict server 400 fallback for cache key and reasoning replay", func(t *testing.T) {
		var calls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			if _, has := body["prompt_cache_key"]; has {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":{"message":"unknown parameter: prompt_cache_key"}}`)
				return
			}
			for _, m := range body["messages"].([]any) {
				if _, has := m.(map[string]any)["reasoning_content"]; has {
					w.WriteHeader(422)
					fmt.Fprint(w, `{"error":{"message":"extra inputs are not permitted: reasoning_content"}}`)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"model":"strict","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
		}))
		defer srv.Close()

		c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "strict", ReplayReasoning: true})
		c.MaxAttempts = 1
		nat, _ := json.Marshal(chatNative{ReasoningContent: "thinking"})
		req := Request{
			CacheKey: "ck-1",
			Messages: []Message{
				{Role: "user", Text: "a"},
				{Role: "assistant", Text: "b", Native: nat, NativeProtocol: ProtocolChat, NativeHost: srv.URL},
				{Role: "user", Text: "c"},
			},
		}
		resp, err := c.Complete(context.Background(), req, nil)
		if err != nil || resp.Text != "ok" {
			t.Fatalf("resp=%+v err=%v", resp, err)
		}
		if calls != 3 || !c.chatNoCacheKey.Load() || !c.chatNoReplayReasoning.Load() {
			t.Fatalf("calls=%d noCacheKey=%v noReplay=%v", calls, c.chatNoCacheKey.Load(), c.chatNoReplayReasoning.Load())
		}
		// Latched flags mean the next call succeeds in a single HTTP request.
		before := calls
		if _, err := c.Complete(context.Background(), req, nil); err != nil {
			t.Fatal(err)
		}
		if calls != before+1 {
			t.Fatalf("expected 1 request after latching, got %d", calls-before)
		}
	})
}

func TestAnthropicCacheControlBreakpoints(t *testing.T) {
	hasCC := func(msg map[string]any) (int, bool) {
		blocks := msg["content"].([]map[string]any)
		for i, b := range blocks {
			if b["cache_control"] != nil {
				return i, true
			}
		}
		return -1, false
	}

	// Single user message still receives a breakpoint (no 1-turn cache miss).
	one := anthropicMessages(Request{Messages: []Message{{Role: "user", Text: "hello"}}}, "")
	if idx, ok := hasCC(one[0]); !ok || idx != 0 {
		t.Fatalf("single message missing cache_control: %v", one)
	}

	// Multi-turn places breakpoints on BOTH msgs[len-2] and msgs[len-1],
	// skipping trailing thinking blocks.
	nativeWithTrailingThinking := json.RawMessage(`[
		{"type":"text","text":"answer"},
		{"type":"thinking","thinking":"hmm","signature":"sig"}
	]`)
	multi := anthropicMessages(Request{Messages: []Message{
		{Role: "user", Text: "q1"},
		{Role: "assistant", Native: nativeWithTrailingThinking, NativeProtocol: ProtocolAnthropic},
		{Role: "user", Text: "q2"},
	}}, "")
	if len(multi) != 3 {
		t.Fatalf("len=%d want 3", len(multi))
	}
	if _, ok := hasCC(multi[0]); ok {
		t.Fatalf("msgs[0] should not carry cache_control: %v", multi[0])
	}
	if idx, ok := hasCC(multi[1]); !ok || idx != 0 {
		t.Fatalf("msgs[1] should place cache_control on text block 0 (skipping thinking): idx=%d ok=%v msg=%v", idx, ok, multi[1])
	}
	if idx, ok := hasCC(multi[2]); !ok || idx != 0 {
		t.Fatalf("msgs[2] should carry cache_control: idx=%d ok=%v msg=%v", idx, ok, multi[2])
	}

	// Assistant turn with only a thinking block must NOT get cache_control on thinking.
	onlyThinking := json.RawMessage(`[{"type":"thinking","thinking":"only","signature":"sig"}]`)
	thOnly := anthropicMessages(Request{Messages: []Message{
		{Role: "user", Text: "q1"},
		{Role: "assistant", Native: onlyThinking, NativeProtocol: ProtocolAnthropic},
		{Role: "user", Text: "q2"},
	}}, "")
	if _, ok := hasCC(thOnly[1]); ok {
		t.Fatalf("thinking-only message must not carry cache_control: %v", thOnly[1])
	}
	if _, ok := hasCC(thOnly[2]); !ok {
		t.Fatalf("final user message should still carry cache_control: %v", thOnly[2])
	}
}
