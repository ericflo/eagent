package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A call left without a result in one conversation file is not listed when
// a new file has started: a result written now lands in the new
// conversation, which does not hold the call, so it would answer nothing
// there and wake eagent as if a turn of the new conversation was cut off.
func TestCallOpenInAnEarlierFileIsNotDanglingInANewOne(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hello"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "relay_send", `{"text":"go"}`)}, SeenSeq: 3}))
	b.add(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "rollover", NextFile: "2.jsonl"}))
	b.add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "2.jsonl", Index: 1, Reason: "rollover"}))
	b.add(event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "MAP ...", CoversSeq: 4}))
	if d := Replay(b.events).DanglingCalls(event.ActorEagent); len(d) != 0 {
		t.Fatalf("a call of the previous file is listed as dangling in the new one: %+v", d)
	}
	// A call of the new file's own reply still is.
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call("c2", "relay_screen", `{}`)}, SeenSeq: 7}))
	if d := Replay(b.events).DanglingCalls(event.ActorEagent); len(d) != 1 || d[0].ID != "c2" {
		t.Fatalf("the new file's open call: %+v", d)
	}
}

// A call that was in flight when the route changed is priced on the route
// that served it, which its reply names, not on the one that replaced it.
func TestReplyServedByTheOldRouteIsCountedOnIt(t *testing.T) {
	const hostA, hostB = "https://a.example/v1", "https://b.example/v1"
	b := newBuilder()
	b.add(event.New(event.Route, event.ActorHarness, event.RouteData{Actor: event.ActorEagent, BaseURL: hostA, Model: "model-a"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{Model: "model-a-2026", Host: hostA, Usage: event.Usage{Input: 100}}))
	b.add(event.New(event.Route, event.ActorHarness, event.RouteData{Actor: event.ActorEagent, BaseURL: hostB, Model: "model-b"}))
	// The call that started before the swap comes back after it.
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{Model: "model-a-2026", Host: hostA, Usage: event.Usage{Input: 10}}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{Model: "model-b", Host: hostB, Usage: event.Usage{Input: 1}}))
	// A reply that names no host (an older record) goes on the route in use.
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{Usage: event.Usage{Input: 1000}}))
	st := Replay(b.events)
	a := st.ByRoute[RouteKey{Actor: event.ActorEagent, Host: hostA, Model: "model-a"}]
	bb := st.ByRoute[RouteKey{Actor: event.ActorEagent, Host: hostB, Model: "model-b"}]
	if a.Input != 110 || bb.Input != 1001 || len(st.ByRoute) != 2 {
		t.Fatalf("by route: %+v", st.ByRoute)
	}
}
