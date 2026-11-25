package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// A reply sent back for another try replays its native items only to the
// model that wrote them, like a recorded one.
func TestASentBackReplyNamesTheModelThatWroteIt(t *testing.T) {
	resp := &llm.Response{Protocol: llm.ProtocolAnthropic, BaseURL: "https://api.anthropic.com/v1", Model: "claude-opus-5-5",
		Native: []byte(`[{"type":"text","text":"x"}]`), ToolCalls: []event.ToolCall{{ID: "toolu_1", Name: "hold"}}}
	if got := sentBack(resp, "why")[0].NativeModel; got != "claude-opus-5-5" {
		t.Fatalf("NativeModel = %q", got)
	}
}
