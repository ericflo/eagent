package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// thinkingRuleServer is a fake Anthropic server that holds budgeted
// thinking to its rule: a request that asks for thinking is refused when
// its last assistant turn does not start with a thinking block. Every
// reply it gives is one tool call without thinking.
type thinkingRuleServer struct {
	mu     sync.Mutex
	bodies []string
	srv    *httptest.Server
}

func newThinkingRuleServer(t *testing.T) *thinkingRuleServer {
	s := &thinkingRuleServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(raw))
		n := len(s.bodies)
		s.mu.Unlock()
		var body struct {
			Thinking any `json:"thinking"`
			Messages []struct {
				Role    string           `json:"role"`
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(raw, &body)
		lastAssistantThinks := true
		for i := len(body.Messages) - 1; i >= 0; i-- {
			if body.Messages[i].Role == "assistant" {
				c := body.Messages[i].Content
				lastAssistantThinks = len(c) > 0 && (c[0]["type"] == "thinking" || c[0]["type"] == "redacted_thinking")
				break
			}
		}
		if body.Thinking != nil && !lastAssistantThinks {
			w.WriteHeader(400)
			fmt.Fprint(w, "{\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"messages.1.content.0.type: Expected `thinking` or `redacted_thinking`, but found `tool_use`. When `thinking` is enabled, a final `assistant` message must start with a thinking block (preceeding the lastmost set of `tool_use` and `tool_result` blocks). We recommend you include thinking blocks from previous turns. To avoid this requirement, disable `thinking`.\"}}")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		id := fmt.Sprintf("toolu_%d", n)
		frames := []string{
			`{"type":"message_start","message":{"model":"claude-x","usage":{"input_tokens":5}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"` + id + `","name":"ping","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// posts returns how many requests came since from, and their bodies.
func (s *thinkingRuleServer) since(from int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies[from:]...)
}

func (s *thinkingRuleServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func budgetedClient(url string) *Client {
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: url, Model: "claude-x", APIKey: "k", ReasoningEffort: "high", MaxTokens: 32768})
	c.anthropicBudgeted.Store(true)
	c.MaxAttempts = 1
	return c
}

// rebuiltToolLoop is a conversation whose last assistant turn another
// model wrote, rebuilt from its calls, with its results last.
func rebuiltToolLoop() []Message {
	return []Message{
		{Role: "user", Text: "go"},
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "t1", Name: "ping", Args: json.RawMessage(`{}`)}}},
		{Role: "tool", Results: []ToolResult{{CallID: "t1", Name: "ping", Output: "pong"}}},
	}
}

// The reply of the retry without thinking has no thinking block either, so
// it is the last assistant turn of the next request of the same tool loop.
// Having been refused once, the client sends that request without thinking
// at the first try, the same bytes the retry would have sent.
func TestNextToolLoopTurnAfterARetryWithoutThinkingIsNotRefusedAgain(t *testing.T) {
	s := newThinkingRuleServer(t)
	c := budgetedClient(s.srv.URL)
	msgs := rebuiltToolLoop()
	resp, err := c.Complete(context.Background(), Request{Messages: msgs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.count(); got != 2 {
		t.Fatalf("the first refusal should still be paid once: %d posts", got)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(resp.ToolCalls))
	}
	retried := s.since(1)[0]
	// The next turn of the same tool loop, as the harness records it.
	msgs = append(msgs,
		Message{Role: "assistant", ToolCalls: resp.ToolCalls, Native: resp.Native, NativeProtocol: ProtocolAnthropic, NativeModel: resp.Model},
		Message{Role: "tool", Results: []ToolResult{{CallID: resp.ToolCalls[0].ID, Name: "ping", Output: "pong"}}},
	)
	before := s.count()
	if _, err := c.Complete(context.Background(), Request{Messages: msgs}, nil); err != nil {
		t.Fatal(err)
	}
	next := s.since(before)
	if len(next) != 1 {
		t.Fatalf("the next turn of the loop took %d posts; it was refused again", len(next))
	}
	if strings.Contains(next[0], `"thinking"`) {
		t.Fatal("the next turn asked for thinking")
	}
	// Only the new turn was added: the head of the request is what the
	// retry sent, so the cache serves it.
	var a, b map[string]any
	_ = json.Unmarshal([]byte(retried), &a)
	_ = json.Unmarshal([]byte(next[0]), &b)
	delete(a, "messages")
	delete(b, "messages")
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	if string(ra) != string(rb) {
		t.Fatalf("the request parameters changed from the retry:\n%s\n%s", ra, rb)
	}
}

// Leaving thinking out up front is only for that shape. A request whose last
// message is the person's thinks as before, and so does one whose last
// assistant turn starts with a thinking block; a client that was never
// refused asks for thinking on the same shape.
func TestThinkingIsLeftOutUpFrontOnlyForARefusedToolLoopShape(t *testing.T) {
	s := newThinkingRuleServer(t)
	fresh := budgetedClient(s.srv.URL)
	if _, err := fresh.Complete(context.Background(), Request{Messages: rebuiltToolLoop()}, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.since(0); len(got) != 2 || !strings.Contains(got[0], `"thinking"`) {
		t.Fatalf("a client never refused should ask for thinking first: %d posts", len(got))
	}
	// fresh has now been refused once.
	person := append(rebuiltToolLoop(), Message{Role: "user", Text: "and now?"})
	before := s.count()
	_, _ = fresh.Complete(context.Background(), Request{Messages: person}, nil)
	if got := s.since(before); len(got) == 0 || !strings.Contains(got[0], `"thinking"`) {
		t.Fatal("a request after the person's message should ask for thinking first")
	}
	signed := json.RawMessage(`[{"type":"thinking","thinking":"hm","signature":"sig"},{"type":"tool_use","id":"t1","name":"ping","input":{}}]`)
	thought := []Message{
		{Role: "user", Text: "go"},
		{Role: "assistant", ToolCalls: []event.ToolCall{{ID: "t1", Name: "ping", Args: json.RawMessage(`{}`)}}, Native: signed, NativeProtocol: ProtocolAnthropic, NativeModel: "claude-x"},
		{Role: "tool", Results: []ToolResult{{CallID: "t1", Name: "ping", Output: "pong"}}},
	}
	before = s.count()
	if _, err := fresh.Complete(context.Background(), Request{Messages: thought}, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.since(before); len(got) != 1 || !strings.Contains(got[0], `"thinking":{`) {
		t.Fatal("a tool loop whose last turn thought should ask for thinking")
	}
}
