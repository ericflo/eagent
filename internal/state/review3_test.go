package state

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// toolOutputs lists every tool result of a rendered conversation, in order,
// and the lines that carry the pictures the calls brought back.
func toolOutputs(msgs []llm.Message) (outs, pics []string) {
	for _, m := range msgs {
		for _, r := range m.Results {
			outs = append(outs, r.Output)
		}
		if m.Role == "user" && len(m.Images) > 0 && strings.HasPrefix(m.Text, "[image from view_image") {
			pics = append(pics, m.Text+" "+m.Images[0].Path)
		}
	}
	return outs, pics
}

// Some providers number their tool calls from 0 in every response, so two
// replies carry the same call id. Each result stays with the reply that
// made the call, with its pictures, and a later result never rewrites the
// conversation that was already sent.
func TestReusedCallIDKeepsEachResultWithItsOwnCall(t *testing.T) {
	const id = "functions.relay_screen:0"
	pic := func(name string) []event.Attachment {
		return []event.Attachment{{Name: name, Path: "/s/" + name, ContentType: "image/png"}}
	}
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "what is on the screen?"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call(id, "relay_screen", `{}`)}, SeenSeq: 3}))
	b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: id, Name: "relay_screen", Output: "first screen", Images: pic("one.png")}))
	first := Replay(b.events).View(nil)
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call(id, "relay_screen", `{}`)}, SeenSeq: 5}))
	b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: id, Name: "relay_screen", Output: "second screen", Images: pic("two.png")}))
	second := Replay(b.events).View(nil)

	if len(second) <= len(first) || !reflect.DeepEqual(first, second[:len(first)]) {
		t.Fatalf("the second read changed what was already sent:\n%+v\nbecame\n%+v", first, second)
	}
	outs, pics := toolOutputs(second)
	if want := []string{"first screen", "second screen"}; !reflect.DeepEqual(outs, want) {
		t.Fatalf("results = %q, want %q", outs, want)
	}
	if len(pics) != 2 || !strings.Contains(pics[0], "one.png") || !strings.Contains(pics[1], "two.png") {
		t.Fatalf("pictures = %q", pics)
	}

	// A call that was cut off before its result is not given the result of
	// an earlier call with the same id.
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call(id, "relay_screen", `{}`)}, SeenSeq: 7}))
	st := Replay(b.events)
	outs, _ = toolOutputs(st.View(nil))
	if len(outs) != 3 || !strings.Contains(outs[2], "no result") {
		t.Fatalf("a call with no result was given one: %q", outs)
	}
	// And a resume finds it, to close it on the record.
	if d := st.DanglingCalls(event.ActorEagent); len(d) != 1 || d[0].ID != id {
		t.Fatalf("calls left without a result = %+v, want the last one", d)
	}
}

// Two calls of one reply with the same id take its results in the order
// they were recorded.
func TestOneReplysRepeatedCallIDTakesItsResultsInOrder(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "look twice"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call("x", "relay_screen", `{}`), call("x", "session_search", `{"query":"a"}`)}, SeenSeq: 3}))
	b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: "x", Name: "relay_screen", Output: "the screen"}))
	b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: "x", Name: "session_search", Output: "the hits"}))
	outs, _ := toolOutputs(Replay(b.events).View(nil))
	if want := []string{"the screen", "the hits"}; !reflect.DeepEqual(outs, want) {
		t.Fatalf("results = %q, want %q", outs, want)
	}
}

// restarted builds a log whose first request was refused: the user's first
// message, then a conversation opened from a briefing.
func restarted(d event.DossierData) *State {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{
		Text: "@e the heron lands at dusk", Held: "eagent",
		Attachments: []event.Attachment{{Name: "pond.png", Path: "/s/pond.png", ContentType: "image/png"}},
	}))
	b.add(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "rollover", NextFile: "2.jsonl"}))
	b.add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "2.jsonl", Index: 1, Reason: "rollover"}))
	b.add(event.New(event.Dossier, event.ActorHarness, d))
	return Replay(b.events)
}

// A briefing written before any call had seen anything took in no event.
// The conversation it opens shows the whole record after it: the message
// that set the restart off, with its picture.
func TestBriefingThatTookInNothingIsFollowedByTheWholeRecord(t *testing.T) {
	msgs := restarted(event.DossierData{Text: "the briefing", By: "eagent", CoversNone: true}).View(nil)
	var text string
	pictures := 0
	for _, m := range msgs {
		text += m.Text + "\n"
		pictures += len(m.Images)
	}
	if !strings.Contains(text, "the heron lands at dusk") || pictures != 1 {
		t.Fatalf("the first message did not follow the briefing (pictures %d):\n%s", pictures, text)
	}
	if !strings.Contains(msgs[0].Text, "the briefing") {
		t.Fatalf("the conversation does not open with the briefing:\n%s", msgs[0].Text)
	}
}

// An older log's briefing does not say what it took in. Its conversation
// shows its own file, as it always did: the briefing accounts for the rest.
func TestBriefingWithoutCoverageShowsItsOwnFile(t *testing.T) {
	var text string
	for _, m := range restarted(event.DossierData{Text: "the briefing", By: "eagent"}).View(nil) {
		text += m.Text + "\n"
	}
	if strings.Contains(text, "the heron lands at dusk") {
		t.Fatalf("an old briefing was followed by the file before it:\n%s", text)
	}
}
