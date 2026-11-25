package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A turn's native items replay only to the model that made them, so the
// rendered turn says which model that was.
func TestARenderedTurnNamesTheModelThatMadeIt(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "status?"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{Provider: "anthropic", Model: "claude-opus-5-5-20261101", Host: "https://api.anthropic.com/v1",
		ToolCalls: []event.ToolCall{call("toolu_1", "relay_screen", `{}`)}, Native: []byte(`[{"type":"text","text":"x"}]`), SeenSeq: 2}))
	b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: "toolu_1", Name: "relay_screen", Output: "idle"}))
	for _, m := range Replay(b.events).View(nil) {
		if m.Role == "assistant" {
			if m.NativeModel != "claude-opus-5-5-20261101" {
				t.Fatalf("NativeModel = %q", m.NativeModel)
			}
			return
		}
	}
	t.Fatal("no assistant turn rendered")
}
