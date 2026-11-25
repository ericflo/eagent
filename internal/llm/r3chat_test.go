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
)

var r3ChatTools = []Tool{
	{Name: "send_message", Parameters: json.RawMessage(`{"type":"object"}`)},
	{Name: "session_read", Parameters: json.RawMessage(`{"type":"object"}`)},
	{Name: "session_search", Parameters: json.RawMessage(`{"type":"object"}`)},
	{Name: "relay_screen", Parameters: json.RawMessage(`{"type":"object"}`)},
	{Name: "aa", Parameters: json.RawMessage(`{"type":"object"}`)},
}

func namedCall(index int, id, name, args string) string {
	call := map[string]any{"index": index, "function": map[string]any{"name": name, "arguments": args}}
	if id != "" {
		call["id"] = id
		call["type"] = "function"
	}
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{call}}}}})
	return string(raw)
}

const chatToolsDone = `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`

func chatCallNames(t *testing.T, chunks ...string) []string {
	t.Helper()
	srv := chatSSE(append(chunks, chatToolsDone)...)
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	c.MaxAttempts = 1
	resp, err := c.Complete(context.Background(), Request{Tools: r3ChatTools, Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tc := range resp.ToolCalls {
		names = append(names, tc.Name+" "+string(tc.Args))
	}
	return names
}

// A tool name streamed in pieces comes out whole, also when a piece is one
// the name so far already ends with ("send_mes" then "s"); and a provider
// that repeats the whole name on every chunk does not get it doubled.
func TestToolNamesInFragmentsAndRepeatedComeOutRight(t *testing.T) {
	cases := map[string][]string{
		"pieces": {
			namedCall(0, "c1", "send_mes", ""),
			namedCall(0, "", "s", `{"te`),
			namedCall(0, "", "age", `xt":"hi"}`),
			namedCall(1, "c2", "session_", ""),
			namedCall(1, "", "re", ""),
			namedCall(1, "", "ad", `{}`),
			namedCall(2, "c3", "relay_scre", ""),
			namedCall(2, "", "e", ""),
			namedCall(2, "", "n", `{}`),
			namedCall(3, "c4", "a", ""),
			namedCall(3, "", "a", `{}`),
		},
		"whole name on every chunk": {
			namedCall(0, "c1", "send_message", ""),
			namedCall(0, "", "send_message", `{"te`),
			namedCall(0, "", "send_message", `xt":"hi"}`),
			namedCall(1, "c2", "session_read", ""),
			namedCall(1, "", "session_read", `{}`),
			namedCall(2, "c3", "relay_screen", `{}`),
			namedCall(2, "", "relay_screen", ``),
			namedCall(3, "c4", "aa", `{}`),
			namedCall(3, "", "aa", ``),
		},
		"the name so far on every chunk": {
			namedCall(0, "c1", "send", ""),
			namedCall(0, "", "send_mes", `{"te`),
			namedCall(0, "", "send_message", `xt":"hi"}`),
			namedCall(1, "c2", "session_read", `{}`),
			namedCall(2, "c3", "relay_", ""),
			namedCall(2, "", "relay_screen", `{}`),
			namedCall(3, "c4", "aa", `{}`),
		},
	}
	want := `send_message {"text":"hi"}|session_read {}|relay_screen {}|aa {}`
	for name, chunks := range cases {
		if got := strings.Join(chatCallNames(t, chunks...), "|"); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
}

// chatRefusing answers every request with a 400 carrying body, and records
// the requests.
func chatRefusing(t *testing.T, body func(req map[string]any) string) (*httptest.Server, *[]map[string]any) {
	var reqs []map[string]any
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		reqs = append(reqs, req)
		if b := body(req); b != "" {
			w.WriteHeader(400)
			fmt.Fprint(w, b)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

func replayedReasoningRequest() Request {
	native, _ := json.Marshal(chatNative{ReasoningContent: "earlier thought"})
	return Request{CacheKey: "sess", Messages: []Message{
		{Role: "user", Text: "hi"},
		{Role: "assistant", Text: "hello", Native: native, NativeProtocol: ProtocolChat},
		{Role: "user", Text: "again"},
	}}
}

// An error about reasoning_effort is not one about replayed reasoning, and
// a field dropped because an error seemed to be about it comes back when
// the retry without it fails the same way: dropping it did not help, and
// keeping it dropped would cost the cache of every later call.
func TestChatRetriesDoNotDropFieldsForGood(t *testing.T) {
	t.Run("reasoning_effort", func(t *testing.T) {
		srv, reqs := chatRefusing(t, func(req map[string]any) string {
			if req["reasoning_effort"] != nil {
				return `{"error":{"message":"Unrecognized request argument supplied: reasoning_effort"}}`
			}
			return ""
		})
		c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k", ReasoningEffort: "high", ReplayReasoning: true})
		c.MaxAttempts = 1
		_, err := c.Complete(context.Background(), replayedReasoningRequest(), nil)
		if err == nil || !strings.Contains(err.Error(), "reasoning_effort") {
			t.Fatalf("err = %v", err)
		}
		if c.chatNoReplayReasoning.Load() || c.chatNoCacheKey.Load() {
			t.Fatalf("kept dropped: reasoning replay %v, cache key %v", c.chatNoReplayReasoning.Load(), c.chatNoCacheKey.Load())
		}
		for i, r := range *reqs {
			msgs := r["messages"].([]any)
			if i == 0 && msgs[1].(map[string]any)["reasoning_content"] == nil {
				t.Fatalf("the first request carried no replayed reasoning")
			}
		}
	})
	t.Run("a field that was not the problem", func(t *testing.T) {
		srv, reqs := chatRefusing(t, func(map[string]any) string {
			return `{"error":{"message":"Extra inputs are not permitted: messages[2].name"}}`
		})
		c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k", ReplayReasoning: true})
		c.MaxAttempts = 1
		if _, err := c.Complete(context.Background(), replayedReasoningRequest(), nil); err == nil {
			t.Fatal("no error")
		}
		if c.chatNoReplayReasoning.Load() || c.chatNoCacheKey.Load() {
			t.Fatalf("kept dropped: reasoning replay %v, cache key %v", c.chatNoReplayReasoning.Load(), c.chatNoCacheKey.Load())
		}
		// The next call sends both again.
		n := len(*reqs)
		_, _ = c.Complete(context.Background(), replayedReasoningRequest(), nil)
		next := (*reqs)[n]
		if next["prompt_cache_key"] == nil || next["messages"].([]any)[1].(map[string]any)["reasoning_content"] == nil {
			t.Fatalf("the next call left a field out: %v", next)
		}
	})
	t.Run("a field that was the problem stays dropped", func(t *testing.T) {
		srv, _ := chatRefusing(t, func(req map[string]any) string {
			if req["prompt_cache_key"] != nil {
				return `{"error":{"message":"Unrecognized request argument supplied: prompt_cache_key"}}`
			}
			return ""
		})
		c := NewClient(Endpoint{Protocol: ProtocolChat, BaseURL: srv.URL, Model: "m", APIKey: "k", ReplayReasoning: true})
		c.MaxAttempts = 1
		if _, err := c.Complete(context.Background(), replayedReasoningRequest(), nil); err != nil {
			t.Fatal(err)
		}
		if !c.chatNoCacheKey.Load() || c.chatNoReplayReasoning.Load() {
			t.Fatalf("cache key dropped %v, reasoning replay dropped %v", c.chatNoCacheKey.Load(), c.chatNoReplayReasoning.Load())
		}
	})
}
