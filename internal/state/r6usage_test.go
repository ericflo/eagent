package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Silent looks, dossier passes and summaries name the route that served
// them and are priced there; records that name none are priced on the
// route in use, as before. A reply from a route that is no longer the one
// in use does not set the conversation's size.
func TestUsageThatNamesItsRouteIsCountedThere(t *testing.T) {
	const hostA, hostB = "https://a.example/v1", "https://b.example/v1"
	a := RouteKey{Actor: event.ActorEagent, Host: hostA, Model: "model-a"}
	bk := RouteKey{Actor: event.ActorEagent, Host: hostB, Model: "model-b"}
	b := newBuilder()
	b.add(event.New(event.Route, event.ActorHarness, event.RouteData{Actor: event.ActorEagent, BaseURL: hostA, Model: "model-a"}))
	b.add(event.New(event.Checks, event.ActorEagent, event.ChecksData{Count: 1, Usage: event.Usage{Input: 1}, PromptTokens: 1, Host: hostA, Asked: "model-a"}))
	b.add(event.New(event.Route, event.ActorHarness, event.RouteData{Actor: event.ActorEagent, BaseURL: hostB, Model: "model-b"}))
	// A look that was out across the change.
	b.add(event.New(event.Checks, event.ActorEagent, event.ChecksData{Count: 1, Usage: event.Usage{Input: 5678}, PromptTokens: 5678, Host: hostA, Asked: "model-a"}))
	st := Replay(b.events)
	if st.ByRoute[a].Input != 5679 || st.ByRoute[bk].Input != 0 {
		t.Fatalf("by route: %+v", st.ByRoute)
	}
	if got := st.ContextTokens(); got != 1 {
		t.Fatalf("the old model's late look set the prompt size to %d", got)
	}
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{Host: hostA, Asked: "model-a", Usage: event.Usage{Input: 7000}}))
	if got := Replay(b.events).ContextTokens(); got != 1 {
		t.Fatalf("the old model's late reply set the prompt size to %d", got)
	}
	// An older record: no route named, priced on the route in use, and it
	// sets the size as it always did.
	b.add(event.New(event.Checks, event.ActorEagent, event.ChecksData{Count: 2, Usage: event.Usage{Input: 300}, PromptTokens: 150}))
	b.add(event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "MAP", Calls: 2, Usage: &event.Usage{Input: 40}}))
	b.add(event.New(event.ThreadTitle, event.ActorSummarizer, event.ThreadTitleData{Title: "t", By: "summary", Usage: &event.Usage{Input: 9}}))
	st = Replay(b.events)
	if st.ByRoute[bk].Input != 340 || st.ContextTokens() != 150 || st.Calls[event.ActorEagent] != 2+1+2+2 {
		t.Fatalf("an older record: by route %+v, prompt %d, calls %d", st.ByRoute, st.ContextTokens(), st.Calls[event.ActorEagent])
	}
	// A dossier pass the model changed under: each route its own calls.
	b.add(event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "MAP", Calls: 3, Usage: &event.Usage{Input: 20}, Host: hostB, Asked: "model-b",
		Earlier: []event.RouteUsage{{Host: hostA, Asked: "model-a", Usage: event.Usage{Input: 2000}, Calls: 2}}}))
	b.add(event.New(event.ThreadTitle, event.ActorSummarizer, event.ThreadTitleData{Title: "u", By: "summary", Usage: &event.Usage{Input: 5}, Host: hostA, Asked: "model-s"}))
	st = Replay(b.events)
	if st.ByRoute[a].Input != 5679+7000+2000 || st.ByRoute[bk].Input != 360 || st.Calls[event.ActorEagent] != 7+3 {
		t.Fatalf("a pass on two routes: by route %+v, calls %d", st.ByRoute, st.Calls[event.ActorEagent])
	}
	if got := st.ByRoute[RouteKey{Actor: event.ActorSummarizer, Host: hostA, Model: "model-s"}].Input; got != 5 {
		t.Fatalf("the summary on its route: %d; by route %+v", got, st.ByRoute)
	}
}
