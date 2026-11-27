package harness

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/llm"
)

// A model is picked in the thread's settings while the dossier of a
// restart made for a conversation too long from its start is written. The
// new model starts with no restarts in a row counted against it
// (newModel), and the pass that lands after the move does not write the
// old model's count back over that.
func TestAMoveDuringAPassKeepsTheNewModelsCleanCount(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{5 * time.Millisecond})
	route := &r5PictureRoute{window: 60000, ceiling: 2048, picture: 80000}
	inner := route.brain()
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	arrived := make(chan struct{}, 1)
	var passes atomic.Int64
	brain := func(model string, m []map[string]any) reply {
		rep := inner(model, m)
		if dossierRequest(m) != "" && rep.status == 0 && passes.Add(1) == 2 {
			// The second pass: the one after the first restart's
			// conversation was refused on its first call (a rescue).
			rep.block = gate
			arrived <- struct{}{}
		}
		return rep
	}
	s := startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	})
	r5LogOnFailure(s)
	s.settled()
	route.refuseAll.Store(true)
	r5SendPicture(s, "@e what does this screenshot show?")
	select {
	case <-arrived:
	case <-time.After(15 * time.Second):
		t.Fatal("the second pass never went out")
	}
	r5Swap(t, s, 60000, func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		return hold()
	})
	var rescues int
	var retried bool
	s.rt.sync(func() { rescues, retried = s.rt.restartRescues, s.rt.restartRetried })
	if rescues != 0 || retried {
		t.Fatalf("right after the move: %d restarts in a row counted, retried %v; want none", rescues, retried)
	}
	before := r6Dossiers(s)
	release()
	r4Until(t, "the pass to land", func() bool { return r6Dossiers(s) > before })
	s.rt.sync(func() { rescues, retried = s.rt.restartRescues, s.rt.restartRetried })
	if rescues != 0 || retried {
		t.Fatalf("after the pass that began before the move landed: %d restarts in a row counted against the new model, retried %v; want none", rescues, retried)
	}
}

// The same when the move is a fallback: the old model's provider stops
// serving it in the middle of that pass, and the chain's next model
// writes the dossier.
func TestAFallbackDuringAPassKeepsTheNewModelsCleanCount(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{5 * time.Millisecond})
	route := &r5PictureRoute{window: 60000, ceiling: 2048, picture: 80000}
	inner := route.brain()
	var passes atomic.Int64
	var gone atomic.Bool
	brain := func(model string, m []map[string]any) reply {
		if gone.Load() {
			return reply{status: 401, body: `{"error":{"message":"invalid api key","type":"authentication_error"}}`}
		}
		rep := inner(model, m)
		if dossierRequest(m) != "" && rep.status == 0 && passes.Add(1) == 2 {
			gone.Store(true)
			return reply{status: 401, body: `{"error":{"message":"invalid api key","type":"authentication_error"}}`}
		}
		return rep
	}
	fallback := newScripted(func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		return hold()
	})
	t.Cleanup(fallback.srv.Close)
	s := startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
		c.Model.Fallback = &config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: fallback.srv.URL, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 60000, MaxTokens: 2048}
	})
	r5LogOnFailure(s)
	s.settled()
	route.refuseAll.Store(true)
	r5SendPicture(s, "@e what does this screenshot show?")
	r4Until(t, "the second pass, written by the fallback", func() bool { return fallback.count("other-model") > 0 && r6Dossiers(s) >= 2 })
	var rescues int
	var retried bool
	s.rt.sync(func() { rescues, retried = s.rt.restartRescues, s.rt.restartRetried })
	if rescues != 0 || retried {
		t.Fatalf("after the pass the fallback finished: %d restarts in a row counted against it, retried %v; want none", rescues, retried)
	}
}
