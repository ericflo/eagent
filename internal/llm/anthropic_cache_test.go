package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A look ends with a message that is not recorded: the next call carries a
// different one in its place. The block before it must hold a cache
// breakpoint, and everything up to that block must be the same bytes in
// both requests, or every look pays for the whole conversation again.
func TestAnthropicBreakpointSurvivesAReplacedLastMessage(t *testing.T) {
	call := event.ToolCall{ID: "t1", Name: "relay_screen", Args: json.RawMessage(`{}`)}
	history := []Message{
		{Role: "user", Text: "[10:00:00] <user> merge the open PRs"},
		{Role: "user", Text: "[10:00:01 Passed to Codex as written.]"},
		{Role: "assistant", ToolCalls: []event.ToolCall{call}},
		{Role: "tool", Results: []ToolResult{{CallID: "t1", Name: "relay_screen", Output: "the screen"}}},
		{Role: "user", Text: "[10:02:00] <Codex> Listing the open PRs."},
	}
	render := func(tail string) (string, []string) {
		msgs := anthropicMessages(Request{Messages: append(history[:len(history):len(history)], Message{Role: "user", Text: tail})}, "")
		raw, err := json.Marshal(msgs)
		if err != nil {
			t.Fatal(err)
		}
		var marked []string
		for _, m := range msgs {
			for _, b := range m["content"].([]map[string]any) {
				if b["cache_control"] != nil {
					text, _ := b["text"].(string)
					marked = append(marked, text)
				}
			}
		}
		return string(raw), marked
	}
	first, marked := render("[eagent 10:02:30] A look while the work goes on.")
	second, _ := render("[eagent 10:03:15] A look while the work goes on. The user last heard from you 1m ago.")
	if len(marked) != 2 || marked[0] != "[10:02:00] <Codex> Listing the open PRs." || !strings.HasPrefix(marked[1], "[eagent 10:02:30]") {
		t.Fatalf("breakpoints are on %q; want the last recorded line and the end of the prompt", marked)
	}
	// The two requests are the same up to and including the block that
	// holds the breakpoint.
	stable := `"text":"[10:02:00] \u003cCodex\u003e Listing the open PRs.","type":"text"}`
	i := strings.Index(first, stable)
	if i < 0 {
		t.Fatalf("stable block not found in %s", first)
	}
	if end := i + len(stable); len(second) < end || first[:end] != second[:end] {
		t.Fatalf("the requests differ before the breakpoint:\n%s\n%s", first, second)
	}
}

// marked lists the text of every block of a conversation that carries a
// cache breakpoint, in order.
func marked(msgs []map[string]any) []string {
	var out []string
	for _, m := range msgs {
		for _, b := range m["content"].([]map[string]any) {
			if b["cache_control"] != nil {
				text, _ := b["text"].(string)
				out = append(out, text)
			}
		}
	}
	return out
}

// A breakpoint finds an earlier cache entry only about twenty blocks behind
// it. When an agent says more than that between two of eagent's calls, the
// two breakpoints at the end would find nothing and the whole conversation
// would be written to the cache again. A third one, on the block where the
// last call left its entry, keeps the step on the cache.
func TestAnthropicLongStepBetweenCallsStaysOnTheCache(t *testing.T) {
	history := []Message{
		{Role: "user", Text: "[10:00:00] <user> merge the open PRs"},
		{Role: "assistant", Text: "On it."},
		{Role: "user", Text: "[10:02:00] <Codex> Listing the open PRs."},
	}
	with := func(remarks int, tail string) []Message {
		out := history[:len(history):len(history)]
		for i := 0; i < remarks; i++ {
			out = append(out, Message{Role: "user", Text: fmt.Sprintf("[10:03:%02d] <Codex> step %d", i, i)})
		}
		return append(out, Message{Role: "user", Text: tail})
	}
	first := anthropicMessages(Request{Messages: with(0, "[eagent 10:02:30] A look.")}, "")
	prev := anthropicBridge(first, 0)
	if got := marked(first); prev != 4 || len(got) != 2 {
		t.Fatalf("first call: %d blocks, breakpoints on %q", prev, got)
	}
	// A short step needs nothing more: the end is within reach of the
	// entry the last call left.
	short := anthropicMessages(Request{Messages: with(5, "[eagent 10:04:00] A look.")}, "")
	if anthropicBridge(short, prev); len(marked(short)) != 2 {
		t.Fatalf("a short step: breakpoints on %q", marked(short))
	}
	// A long one gets the third breakpoint, on the last recorded block the
	// first call saw (the one before the end of its prompt).
	long := anthropicMessages(Request{Messages: with(30, "[eagent 10:04:00] A look.")}, "")
	n := anthropicBridge(long, prev)
	got := marked(long)
	if n != 34 || len(got) != 3 || got[0] != "[10:02:00] <Codex> Listing the open PRs." || got[1] != "[10:03:29] <Codex> step 29" || !strings.HasPrefix(got[2], "[eagent 10:04:00]") {
		t.Fatalf("a long step: %d blocks, breakpoints on %q", n, got)
	}
	// A count left over from a longer conversation (before a restart from
	// a dossier) marks nothing.
	fresh := anthropicMessages(Request{Messages: with(0, "[eagent 10:05:00] A look.")}, "")
	if anthropicBridge(fresh, 500); len(marked(fresh)) != 2 {
		t.Fatalf("after a restart: breakpoints on %q", marked(fresh))
	}
}

// A request never carries more than the four breakpoints Anthropic allows,
// and the client remembers where the last call's entries are without being
// told.
func TestAnthropicRequestKeepsToFourBreakpoints(t *testing.T) {
	var bodies []string
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		raw, _ := json.Marshal(body)
		bodies = append(bodies, string(raw))
		for _, e := range []string{
			`{"type":"message_start","message":{"model":"claude","usage":{"input_tokens":10}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", e)
		}
	})
	defer srv.Close()
	c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude", APIKey: "k"})
	tools := []Tool{{Name: "hold", Parameters: json.RawMessage(`{"type":"object"}`)}, {Name: "send_message", Parameters: json.RawMessage(`{"type":"object"}`)}}
	msgs := []Message{{Role: "user", Text: "one"}, {Role: "assistant", Text: "two"}, {Role: "user", Text: "three"}, {Role: "user", Text: "the end of the first prompt"}}
	if _, err := c.Complete(context.Background(), Request{System: "sys", Tools: tools, Messages: msgs}, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		msgs = append(msgs, Message{Role: "user", Text: fmt.Sprintf("remark %d", i)})
	}
	if _, err := c.Complete(context.Background(), Request{System: "sys", Tools: tools, Messages: msgs}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Complete(context.Background(), Request{Tools: tools, Messages: msgs[:4]}, nil); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 {
		t.Fatalf("%d requests", len(bodies))
	}
	for i, want := range []int{3, 4, 3} {
		if got := strings.Count(bodies[i], `"cache_control"`); got != want {
			t.Fatalf("request %d carries %d breakpoints, want %d: %s", i, got, want, bodies[i])
		}
	}
	// With a system prompt its breakpoint covers the tools; without one
	// the last tool carries it.
	if strings.Contains(bodies[1], `"name":"send_message"}`) == false || strings.Contains(bodies[1], `"cache_control":{"type":"ephemeral"},"description":"","input_schema":{"type":"object"},"name":"send_message"`) {
		t.Fatalf("the tools carry a breakpoint beside the system prompt's: %s", bodies[1])
	}
	if !strings.Contains(bodies[2], `"cache_control":{"type":"ephemeral"},"description":"","input_schema":{"type":"object"},"name":"send_message"`) {
		t.Fatalf("with no system prompt the last tool carries no breakpoint: %s", bodies[2])
	}
	// The third breakpoint of the long step is on the block before the end
	// of the first prompt.
	if !strings.Contains(bodies[1], `{"cache_control":{"type":"ephemeral"},"text":"three","type":"text"}`) {
		t.Fatalf("the long step has no breakpoint where the first call left its entry: %s", bodies[1])
	}
}

// Every effort the configuration accepts maps to a budget for models that
// take the older budget-style thinking; none is refused before the call.
func TestAnthropicBudgetedThinkingAcceptsEveryEffort(t *testing.T) {
	for _, effort := range []string{"minimal", "low", "medium", "high", "xhigh", "max"} {
		var body map[string]any
		srv := sseServer(t, func(w http.ResponseWriter, b map[string]any) {
			body = b
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", `{"type":"message_start","message":{"model":"claude","usage":{"input_tokens":1}}}`)
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`)
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", `{"type":"message_stop"}`)
		})
		c := NewClient(Endpoint{Protocol: ProtocolAnthropic, BaseURL: srv.URL, Model: "claude", APIKey: "k", ReasoningEffort: effort, MaxTokens: 100000})
		c.anthropicBudgeted.Store(true)
		_, err := c.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
		srv.Close()
		if err != nil {
			t.Fatalf("effort %s: %v", effort, err)
		}
		th, _ := body["thinking"].(map[string]any)
		if th["type"] != "enabled" || th["budget_tokens"] == nil {
			t.Fatalf("effort %s: thinking = %v", effort, body["thinking"])
		}
	}
}
