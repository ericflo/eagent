package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// twoAccounts is a chain whose two entries name one model at one address,
// each with its own key and limits (two accounts or keys).
func twoAccounts(fallbackURL string) config.ModelRoute {
	return config.ModelRoute{
		Protocol: llm.ProtocolChat, BaseURL: "https://openrouter.ai/api/v1", Model: "z-ai/glm-5.3",
		APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 200_000, MaxTokens: 32_000, ReasoningEffort: "high",
		Fallback: &config.ModelRoute{
			Protocol: llm.ProtocolChat, BaseURL: fallbackURL, Model: "z-ai/glm-5.3",
			APIKeyEnv: "EAGENT_TEST_KEY2", ContextTokens: 50_000, MaxTokens: 4_000, ReasoningEffort: "low",
		},
	}
}

func routesSession(t *testing.T, model config.ModelRoute) *Runtime {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_KEY2", "y")
	t.Setenv("OPENROUTER_API_KEY", "fake")
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1")
	cfg.Model = model
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	return rt
}

// moveToFallback moves the session to its second route as complete() does
// when the first cannot serve the call.
func moveToFallback(rt *Runtime) {
	m := rt.models[event.ActorEagent]
	m.at = 1
	m.client = rt.newClient(m.routes[1], event.ActorEagent)
	rt.followRoute(event.ActorEagent)
}

// An effort change on the second of two entries for one model keeps that
// entry's key and limits, also after an earlier pick in the same run.
func TestAnEffortChangeOnTheSecondAccountKeepsItAfterAnEarlierPick(t *testing.T) {
	rt := routesSession(t, twoAccounts("https://openrouter.ai/api/v1"))
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Effort = "medium" }); err != nil {
		t.Fatal(err)
	}
	if m := rt.models[event.ActorEagent]; len(m.routes) != 2 {
		t.Fatalf("routes after the pick: %+v", m.routes)
	}
	moveToFallback(rt)
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Effort = "high" }); err != nil {
		t.Fatal(err)
	}
	ep := rt.models[event.ActorEagent].client.Endpoint
	_, reply := rt.window()
	if ep.APIKey != "y" || ep.MaxTokens != 4_000 || reply != 4_000 || ep.ReasoningEffort != "high" {
		t.Fatalf("after the effort change: key %q, reply ceiling %d (window reply %d) @ %s; want the second account's \"y\" and 4000", ep.APIKey, ep.MaxTokens, reply, ep.ReasoningEffort)
	}
}

// A base URL written with a trailing slash is the same address: an effort
// change on that fallback keeps its key, window and reply ceiling.
func TestAnEffortChangeOnAFallbackWrittenWithATrailingSlashKeepsItsLimits(t *testing.T) {
	model := twoAccounts("https://openrouter.ai/api/v1/")
	model.Fallback.Model = "z-ai/glm-5.3-flash"
	rt := routesSession(t, model)
	moveToFallback(rt)
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Effort = "medium" }); err != nil {
		t.Fatal(err)
	}
	ep := rt.models[event.ActorEagent].client.Endpoint
	tokens, reply := rt.window()
	if ep.APIKey != "y" || tokens != 50_000 || reply != 4_000 || ep.ReasoningEffort != "medium" {
		t.Fatalf("after the effort change: key %q, window %d, reply %d @ %s; want \"y\", 50000 and 4000", ep.APIKey, tokens, reply, ep.ReasoningEffort)
	}
}

// An effort change on the configured model keeps its fallbacks and its
// limits, and so does picking that model again after another one.
func TestAnEffortChangeOnTheConfiguredModelKeepsItsFallbacks(t *testing.T) {
	model := twoAccounts("http://127.0.0.1:2")
	model.Fallback.Model, model.MaxTokens = "eagent-fallback", 32768
	rt := routesSession(t, model)
	check := func(when string) {
		t.Helper()
		m := rt.models[event.ActorEagent]
		if len(m.routes) != 2 || m.routes[0].ReasoningEffort != "low" || m.routes[0].MaxTokens != 32768 || m.routes[1].Model != "eagent-fallback" {
			t.Fatalf("%s: %+v", when, m.routes)
		}
	}
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Effort = "low" }); err != nil {
		t.Fatal(err)
	}
	check("an effort change")
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Model, p.Effort = "moonshotai/kimi-k3", "high" }); err != nil {
		t.Fatal(err)
	}
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Model, p.Effort = "z-ai/glm-5.3", "low" }); err != nil {
		t.Fatal(err)
	}
	check("the configured model picked again")
}
