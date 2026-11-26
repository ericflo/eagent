package harness

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// onFallback builds a session whose configured chain is two OpenRouter
// models, the fallback with limits and a key variable of its own, and
// moves it to the fallback as complete() does when the first route cannot
// serve the call. No client is ever called.
func onFallback(t *testing.T, providerKey string) *Runtime {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_KEY2", "y")
	t.Setenv("OPENROUTER_API_KEY", providerKey) // a fake value or none
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1")
	cfg.Model = config.ModelRoute{
		Protocol: llm.ProtocolChat, BaseURL: "https://openrouter.ai/api/v1", Model: "z-ai/glm-5.3",
		APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 200_000, MaxTokens: 32_000, ReasoningEffort: "high",
		Fallback: &config.ModelRoute{
			Protocol: llm.ProtocolChat, BaseURL: "https://openrouter.ai/api/v1", Model: "z-ai/glm-5.3-flash",
			APIKeyEnv: "EAGENT_TEST_KEY2", ContextTokens: 50_000, MaxTokens: 4_000, ReasoningEffort: "low",
		},
	}
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	m := rt.models[event.ActorEagent]
	m.at = 1
	m.client = rt.newClient(m.routes[1], event.ActorEagent)
	rt.followRoute(event.ActorEagent)
	if got := rt.activeSettings.Routes[runtimecontrol.RouteEagent]; got.Model != "z-ai/glm-5.3-flash" || got.Provider != "openrouter" || got.Effort != "low" {
		t.Fatalf("the live settings show %+v on the fallback", got)
	}
	return rt
}

// pickOnFallback applies a change of eagent's pick, as the app's sheet
// sends it, and returns the error, if any.
func pickOnFallback(rt *Runtime, change func(*runtimecontrol.Route)) error {
	before := rt.activeSettings.Routes
	pick := before[runtimecontrol.RouteEagent]
	change(&pick)
	swaps, err := rt.routeChanges(before, map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: pick})
	if err != nil {
		return err
	}
	// As applyRuntimeSettings does: the values become the session's, then
	// the routes change.
	routes := map[string]runtimecontrol.Route{}
	for k, v := range before {
		routes[k] = v
	}
	routes[runtimecontrol.RouteEagent] = pick
	rt.activeSettings.Routes = routes
	for _, sw := range swaps {
		rt.swapRoute(sw)
	}
	return nil
}

// A change of only the effort of the fallback the sheet shows keeps the
// window and reply ceiling the fallback is configured with.
func TestAnEffortChangeOnAFallbackKeepsItsLimits(t *testing.T) {
	rt := onFallback(t, "fake")
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Effort = "medium" }); err != nil {
		t.Fatalf("an effort change on the model the settings show was refused: %v", err)
	}
	ep := rt.models[event.ActorEagent].client.Endpoint
	tokens, reply := rt.window()
	if ep.Model != "z-ai/glm-5.3-flash" || ep.ReasoningEffort != "medium" || ep.APIKey != "y" {
		t.Fatalf("after the change the session runs %s @ %s", ep.Model, ep.ReasoningEffort)
	}
	if ep.MaxTokens != 4_000 || reply != 4_000 || tokens != 50_000 {
		t.Errorf("after an effort change the fallback asks for %d reply tokens (window %d, reply %d); it is configured with 4000 and 50000", ep.MaxTokens, tokens, reply)
	}
}

// The same change when the fallback's key is in a variable the user named
// and the provider's usual variable is not set: it is taken, on that key.
func TestAnEffortChangeOnAFallbackUsesItsOwnKey(t *testing.T) {
	rt := onFallback(t, "")
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Effort = "medium" }); err != nil {
		t.Fatalf("an effort change on the running fallback was refused: %v", err)
	}
	if ep := rt.models[event.ActorEagent].client.Endpoint; ep.APIKey != "y" || ep.ReasoningEffort != "medium" {
		t.Fatalf("the fallback runs with key %q at %s", ep.APIKey, ep.ReasoningEffort)
	}
}

// Picking the first route's model again while on the fallback goes back to
// that route as configured: its key, its limits, and its fallback after it.
func TestPickingTheFirstModelAgainGoesBackToItsOwnRoute(t *testing.T) {
	rt := onFallback(t, "")
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Model, p.Effort = "z-ai/glm-5.3", "medium" }); err != nil {
		t.Fatal(err)
	}
	m := rt.models[event.ActorEagent]
	ep := m.client.Endpoint
	tokens, reply := rt.window()
	if ep.Model != "z-ai/glm-5.3" || ep.APIKey != "x" || ep.ReasoningEffort != "medium" || ep.MaxTokens != 32_000 || tokens != 200_000 || reply != 32_000 {
		t.Fatalf("back on the first model: %s key %q @ %s, max %d, window %d", ep.Model, ep.APIKey, ep.ReasoningEffort, ep.MaxTokens, tokens)
	}
	if len(m.routes) != 2 || m.at != 0 || m.routes[1].Model != "z-ai/glm-5.3-flash" {
		t.Fatalf("routes %+v at %d", m.routes, m.at)
	}
}

// A model the chain does not have starts from the configured route, as a
// pick always did: the provider's key, the catalog's window, no fallback.
func TestPickingAThirdModelOnAFallbackStartsFromTheConfiguredRoute(t *testing.T) {
	rt := onFallback(t, "fake")
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Model, p.Effort = "moonshotai/kimi-k3", "high" }); err != nil {
		t.Fatal(err)
	}
	m := rt.models[event.ActorEagent]
	ep := m.client.Endpoint
	if ep.Model != "moonshotai/kimi-k3" || ep.APIKey != "fake" || ep.ReasoningEffort != "high" || len(m.routes) != 1 {
		t.Fatalf("on the third model: %s key %q @ %s, %d routes", ep.Model, ep.APIKey, ep.ReasoningEffort, len(m.routes))
	}
	if tokens, _ := rt.window(); tokens != 1_000_000 {
		t.Fatalf("window %d, want the catalog's", tokens)
	}
}

// An effort the fallback's model does not offer is refused, so the app is
// told the change was not made, and the session stays as it was.
func TestAnEffortTheFallbackDoesNotOfferIsRefused(t *testing.T) {
	rt := onFallback(t, "fake")
	err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Effort = "xhigh" })
	if err == nil || !strings.Contains(err.Error(), "does not list effort") {
		t.Fatalf("an effort the model lacks: %v", err)
	}
	if ep := rt.models[event.ActorEagent].client.Endpoint; ep.Model != "z-ai/glm-5.3-flash" || ep.ReasoningEffort != "low" {
		t.Fatalf("the refused change moved the session to %s @ %s", ep.Model, ep.ReasoningEffort)
	}
}

// A route at an address no provider knows (a local server) can have its
// effort changed from the app, and the change is made; a change of its
// model, which has no catalog to come from, is refused with a reason.
func TestAChangeToARouteNoProviderKnowsIsMadeOrRefused(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1")
	cfg.Model.ReasoningEffort, cfg.Model.MaxTokens, cfg.Model.ContextTokens = "low", 2048, 16_384
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	if p := rt.activeSettings.Routes[runtimecontrol.RouteEagent]; p.Provider != "" {
		t.Fatalf("the test address has a provider: %+v", p)
	}
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Effort = "high" }); err != nil {
		t.Fatalf("an effort change on a local route: %v", err)
	}
	ep := rt.models[event.ActorEagent].client.Endpoint
	tokens, reply := rt.window()
	if ep.ReasoningEffort != "high" || ep.Model != testModel || ep.MaxTokens != 2048 || tokens != 16_384 || reply != 2048 {
		t.Fatalf("after the change: %s @ %s, max %d, window %d", ep.Model, ep.ReasoningEffort, ep.MaxTokens, tokens)
	}
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Effort = "turbo" }); err == nil {
		t.Fatal("an unknown effort level was taken")
	}
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Model = "some-other-model" }); err == nil {
		t.Fatal("a new model at an address no provider knows was taken")
	}
	if ep := rt.models[event.ActorEagent].client.Endpoint; ep.Model != testModel {
		t.Fatalf("a refused change moved the session to %s", ep.Model)
	}
}
