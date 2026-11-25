package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/state"
)

// Looks that left no record of their own are kept as a batch and written
// later. A route change writes them first, so their cost is counted on the
// route that made them, not on the one that replaced it.
func TestRouteChangeCountsPendingLooksOnTheOldRoute(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("OPENROUTER_API_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1") // clients are swapped, never called
	cfg.Model.Fallback = &config.ModelRoute{Protocol: "openai-chat", BaseURL: "http://127.0.0.1:2", Model: "eagent-fallback", APIKeyEnv: "EAGENT_TEST_KEY"}
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	old := state.RouteKey{Actor: event.ActorEagent, Host: rt.st.Hosts[event.ActorEagent], Model: rt.st.Models[event.ActorEagent]}
	rt.turns.silent = event.ChecksData{Count: 2, Usage: event.Usage{Input: 5000}}
	before := runtimecontrol.FromConfig(cfg).Routes
	after := map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: {Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "high"}}
	swaps, err := rt.routeChanges(before, after)
	if err != nil || len(swaps) != 1 {
		t.Fatalf("new pick: %+v %v", swaps, err)
	}
	rt.swapRoute(swaps[0])
	rt.flushSilent()
	if got := rt.st.ByRoute[old].Input; got != 5000 {
		t.Fatalf("the looks made on %+v are counted %d there; by route: %+v", old, got, rt.st.ByRoute)
	}
}
