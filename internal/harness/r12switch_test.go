package harness

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// A model is picked in the thread's settings while the dossier of a
// restart made for a conversation too long from its start is written. The
// new model starts with no restarts in a row counted against it
// (restartOf), and the pass that lands after the move does not write the
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
	s.rt.sync(func() { _, retried, rescues = s.rt.restartOf() })
	if rescues != 0 || retried {
		t.Fatalf("right after the move: %d restarts in a row counted, retried %v; want none", rescues, retried)
	}
	before := r6Dossiers(s)
	release()
	r4Until(t, "the pass to land", func() bool { return r6Dossiers(s) > before })
	s.rt.sync(func() { _, retried, rescues = s.rt.restartOf() })
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
	s.rt.sync(func() { _, retried, rescues = s.rt.restartOf() })
	if rescues != 0 || retried {
		t.Fatalf("after the pass the fallback finished: %d restarts in a row counted against it, retried %v; want none", rescues, retried)
	}
}

// afterNotesNotice opens what turnFailed tells the user when the
// provider refuses the conversation as too long after a restart.
const afterNotesNotice = "My model refuses my conversation as too long, even after I restarted it from my notes"

// r12Failed waits until the loop has told the user of the model in use
// failing (failTold), which a move to another model sets back (newModel).
func r12Failed(t *testing.T, s *session) {
	t.Helper()
	r4Until(t, "two failed calls on the model in use", func() bool {
		ok := false
		s.rt.sync(func() { ok = s.rt.turns.failTold })
		return ok
	})
}

// After no restart could make room on a model whose reply ceiling took a
// large part of its window, the person picks a model with the same window
// and a smaller ceiling: it has more room for the conversation, so the
// decision found on the old one does not stand on it. It refuses the
// conversation as it is, one restart on it makes room, that restart is
// made, and the message is answered.
func TestAModelWithMoreRoomForInputGetsItsOwnRestart(t *testing.T) {
	s, _ := r5FullSession(t)
	var restarted atomic.Bool
	conf := config.ModelRoute{Protocol: llm.ProtocolChat, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 60000, MaxTokens: 1024}
	srv := newScripted(func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			restarted.Store(true)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		if !restarted.Load() {
			return r7TooLong
		}
		if m[len(m)-1]["role"] != "tool" {
			return say("Here it is.")
		}
		return hold()
	})
	t.Cleanup(srv.srv.Close)
	conf.BaseURL = srv.srv.URL
	eps, err := conf.Routes()
	if err != nil {
		t.Fatal(err)
	}
	before := r6Dossiers(s)
	s.rt.sync(func() {
		s.rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: "other-model"}, eps: eps, conf: conf})
		s.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"})
	})
	r4Until(t, "an answer, or the message told it waits", func() bool {
		return r5Told(s, "Here it is.") > 0 || r5Told(s, stillFullNotice+`"still there?"`) > 0
	})
	if r5Told(s, "Here it is.") == 0 {
		t.Fatalf("on the model with more room (asked %d times): %d restarts, and the user was told their message waits; one restart on it makes room", srv.count("other-model"), r6Dossiers(s)-before)
	}
}

// After no restart could make room, the person picks a model with no more
// room, which refuses the conversation too. The decision was the old
// model's and does not stand on the new one: the new model gets its own
// restart, one, after which no restart can make room on it either and the
// restarts stop. Each time the person hears that no restart can make room,
// or that the conversation is refused "even after I restarted it from my
// notes", a restart was made on the model in use: once on each. (What they
// wrote after the move is in the new model's conversation, and the full
// notice there says it is kept.)
func TestAMoveDoesNotRepeatTheRefusedAfterARestartNotice(t *testing.T) {
	s, _ := r5FullSession(t)
	r12Failed(t, s)
	if n := r5Told(s, afterNotesNotice); n != 1 {
		t.Fatalf("on the first model the user was told %d times that it refuses the conversation after a restart, want once", n)
	}
	before := r6Dossiers(s)
	small := newScripted(func(string, []map[string]any) reply { return r7TooLong })
	t.Cleanup(small.srv.Close)
	conf := config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: small.srv.URL, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 60000, MaxTokens: 2048}
	eps, err := conf.Routes()
	if err != nil {
		t.Fatal(err)
	}
	s.rt.sync(func() {
		s.rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: "other-model"}, eps: eps, conf: conf})
		s.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"})
	})
	r12Failed(t, s)
	calls := small.count("other-model")
	r4Until(t, "more refused calls on the new model", func() bool { return small.count("other-model") > calls+4 })
	if n := r6Dossiers(s) - before; n != 1 {
		t.Fatalf("%d restarts on a model with no more room, want its own one", n)
	}
	if n := r5Told(s, fullNotice); n != 2 {
		t.Fatalf("the user was told %d times that no restart can make room, want twice (once on each model, after its restart)", n)
	}
	if n := r5Told(s, afterNotesNotice); n != 2 {
		t.Fatalf("the user was told %d times that the conversation is refused after a restart, want twice (once on each model, after its restart)", n)
	}
}
