package llm

import (
	"context"
	"encoding/json"
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
	}})
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
	msgs := chatMessages(Request{Messages: []Message{
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "c1", Name: "delegate", Args: normalizeArgs("{")}}},
		{Role: "tool", Results: []ToolResult{{CallID: "c1", Output: "rejected"}}},
	}})
	fn := msgs[0]["tool_calls"].([]map[string]any)[0]["function"].(map[string]any)
	if !json.Valid([]byte(fn["arguments"].(string))) {
		t.Fatalf("replayed arguments are not valid JSON: %s", fn["arguments"])
	}
	items := responsesInput(Request{Messages: []Message{
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "c1", Name: "delegate", Args: normalizeArgs("{")}}},
	}})
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
	}})
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
