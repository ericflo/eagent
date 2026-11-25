package llm

import (
	"encoding/json"
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
