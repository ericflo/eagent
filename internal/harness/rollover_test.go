package harness

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// With no number set, the conversation restarts at half the context window
// of the model its calls go to: the configured one, a fallback once the
// route has moved to it, or the one a runtime setting picked. A number in
// the configuration holds whatever the model.
func TestRolloverFollowsTheModelInUse(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("OPENROUTER_API_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1") // clients are swapped, never called
	cfg.RolloverTokens = 0
	cfg.Model.ContextTokens, cfg.Model.MaxTokens = 200_000, 32768
	cfg.Model.Fallback = &config.ModelRoute{Protocol: "openai-chat", BaseURL: "http://127.0.0.1:2", Model: "eagent-fallback", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 1_000_000, MaxTokens: 32768}
	rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	if got := rt.rolloverTokens(); got != 100_000 {
		t.Fatalf("a 200k-window model restarts at %d, want 100000", got)
	}
	// The route moves to the fallback, which has a 1M window.
	m := rt.models[event.ActorEagent]
	m.client = rt.newClient(m.routes[1], event.ActorEagent)
	if got := rt.rolloverTokens(); got != 500_000 {
		t.Fatalf("on the 1M-window fallback it restarts at %d, want 500000", got)
	}
	if w, _ := rt.window(); w != 1_000_000 {
		t.Fatalf("window = %d on the fallback", w)
	}
	// A runtime setting picks a 200k-window model: the size follows it.
	before := runtimecontrol.FromConfig(cfg).Routes
	after := map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: {Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "high"}}
	swaps, err := rt.routeChanges(before, after)
	if err != nil || len(swaps) != 1 {
		t.Fatalf("pick: %+v %v", swaps, err)
	}
	rt.swapRoute(swaps[0])
	if got := rt.rolloverTokens(); got != 100_000 {
		t.Fatalf("after picking a 200k-window model it restarts at %d, want 100000", got)
	}
	// And a 1M-window one.
	after2 := map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: {Provider: "openrouter", Model: "moonshotai/kimi-k3", Effort: "high"}}
	swaps, err = rt.routeChanges(after, after2)
	if err != nil || len(swaps) != 1 {
		t.Fatalf("second pick: %+v %v", swaps, err)
	}
	rt.swapRoute(swaps[0])
	if got := rt.rolloverTokens(); got != 500_000 {
		t.Fatalf("after picking a 1M-window model it restarts at %d, want 500000", got)
	}
	// A number in the configuration holds whatever the model.
	rt.cfg.RolloverTokens = 150_000
	if got := rt.rolloverTokens(); got != 150_000 {
		t.Fatalf("a configured 150000 gave %d", got)
	}
}

// End to end with no number set: a prompt under half the window carries
// on, and one over it restarts the conversation from a dossier.
func TestConversationRestartsAtHalfTheWindowByDefault(t *testing.T) {
	var prompt atomic.Int64
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			return reply{text: briefing(places(ask)[:3])}
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			r = say("Here.")
		}
		if p := prompt.Swap(0); p > 0 {
			r.prompt = int(p)
		}
		return r
	}, func(c *config.Config) {
		// Half of 80,000 is 40,000, and the rest leaves the restart its room.
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 80_000, 4096
	})
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	var limit int
	s.rt.sync(func() { limit = s.rt.rolloverTokens() })
	if limit != 40_000 {
		t.Fatalf("an 80k window restarts at %d, want 40000", limit)
	}
	prompt.Store(25_000)
	s.type_("@e hello")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	if n := len(s.dossiers()); n != 0 {
		t.Fatalf("%d dossier(s) at 25,000 of an 80,000 window", n)
	}
	prompt.Store(41_000)
	s.type_("@e and again")
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
}
