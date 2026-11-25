package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A log written before results were paired with their reply, by a provider
// that reuses call ids ("functions.send_message:0" in every response). The
// process died under the second reply's call; the resume of that time
// matched the id to the first reply's result, so it wrote nothing. Later
// turns went on as usual.
//
// On every resume since the pairing changed, closeInterrupted asks
// DanglingCalls, writes an "interrupted" result for what it lists, and wakes
// eagent with "You were restarted in the middle of a turn". A result
// written at the end of the log can only answer the newest reply, so a
// call of an older reply can never be closed: it must not be listed at
// all, or every resume of this session writes another result and buys
// another turn, for a turn that ended long ago.
func TestOldReplysCallIsNotDanglingForever(t *testing.T) {
	const id = "functions.send_message:0"
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hello"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call(id, "send_message", `{"text":"hi"}`)}, SeenSeq: 1}))
	b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: id, Name: "send_message", Output: "sent"}))
	b.add(event.New(event.TurnEnd, event.ActorEagent, event.TurnData{Reason: "done"}))
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "again"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call(id, "send_message", `{"text":"hi again"}`)}, SeenSeq: 5}))
	// The process died here; the old resume found no dangling call.
	b.add(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{}))
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "and now?"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call(id, "send_message", `{"text":"here"}`)}, SeenSeq: 8}))
	b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: id, Name: "send_message", Output: "sent"}))
	b.add(event.New(event.TurnEnd, event.ActorEagent, event.TurnData{Reason: "done"}))

	for run := 1; run <= 3; run++ {
		d := Replay(b.events).DanglingCalls(event.ActorEagent)
		if len(d) == 0 {
			return
		}
		if run == 3 {
			t.Fatalf("after %d resumes the old reply's call is still dangling (%d results written for it, each one with a wake): %+v", run-1, run-1, d)
		}
		// What closeInterrupted writes on this resume.
		for _, c := range d {
			b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: c.ID, Name: c.Name, Output: "interrupted: eagent restarted before this finished", IsError: true}))
		}
		b.add(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{}))
	}
}
