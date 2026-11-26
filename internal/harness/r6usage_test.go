package harness

import (
	"context"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/state"
)

// usageModel holds while it reports prompt tokens: the primary's looks
// report 5678 and wait for release when it is not nil; every other model
// reports 100.
func usageModel(t *testing.T, release <-chan struct{}) *scripted {
	s := newScripted(func(model string, _ []map[string]any) reply {
		if model == testModel {
			rep := hold()
			rep.prompt, rep.block = 5678, release
			return rep
		}
		rep := hold()
		rep.prompt = 100
		return rep
	})
	t.Cleanup(s.srv.Close)
	return s
}

// switchTo moves eagent to model at url, as a change in the settings does.
func switchTo(rt *Runtime, url, model string) {
	rt.sync(func() {
		ep := rt.models[event.ActorEagent].client.Endpoint
		ep.BaseURL, ep.Model = url, model
		rt.swapRoute(routeSwap{who: event.ActorEagent, eps: []llm.Endpoint{ep}, conf: config.ModelRoute{BaseURL: url, Model: model}})
	})
}

// silentLook is a look that ends in a hold, as the turn records it.
func silentLook(t *testing.T, rt *Runtime, ctx context.Context) <-chan netOutcome {
	ch := netCallAsync(rt, ctx)
	out := make(chan netOutcome, 1)
	go func() {
		o := <-ch
		if o.err == nil {
			rt.sync(func() { rt.noteSilent(o.resp, rt.st.LastSeq(), wake{}) })
		}
		out <- o
	}()
	return out
}

func byRoute(rt *Runtime, host, model string) event.Usage {
	var u event.Usage
	rt.sync(func() { u = rt.st.ByRoute[state.RouteKey{Actor: event.ActorEagent, Host: host, Model: model}] })
	return u
}

// A silent look that was out when the model was switched is counted on
// the route that served it, not on the new one.
func TestALookOutAcrossASwitchIsCountedOnTheRouteThatServedIt(t *testing.T) {
	release := make(chan struct{})
	model := usageModel(t, release)
	other := usageModel(t, nil)
	rt, _ := netRuntime(t, model.srv.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := silentLook(t, rt, ctx)
	waitFor(t, "the look", func() bool { return model.count(testModel) == 1 })
	switchTo(rt, other.srv.URL, "eagent-other")
	close(release)
	if o := netWait(t, ch); o.err != nil {
		t.Fatal(o.err)
	}
	var late int
	rt.sync(func() { late = rt.turns.promptTokens })
	if late == 5678 {
		t.Fatal("the old model's late look set the new model's prompt size")
	}
	// The new model's own look starts a batch of its own.
	if o := netWait(t, silentLook(t, rt, ctx)); o.err != nil {
		t.Fatal(o.err)
	}
	rt.sync(rt.flushSilent)
	var prompt, size int
	rt.sync(func() { prompt, size = rt.turns.promptTokens, rt.st.ContextTokens() })
	if prompt != 100 || size != 100 {
		t.Fatalf("the prompt size is %d (record %d), not the new model's 100", prompt, size)
	}
	if got, moved := byRoute(rt, model.srv.URL, testModel).Input, byRoute(rt, other.srv.URL, "eagent-other").Input; got != 5678 || moved != 100 {
		t.Fatalf("the old route has %d input tokens, the new one %d", got, moved)
	}
	checks := checksEvents(rt)
	if len(checks) != 2 || checks[0].Asked != testModel || checks[1].Asked != "eagent-other" || checks[0].Host != model.srv.URL {
		t.Fatalf("checks %+v", checks)
	}
}

func checksEvents(rt *Runtime) []event.ChecksData {
	var out []event.ChecksData
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.ChecksData
			if ev.Type == event.Checks && ev.Decode(&d) == nil {
				out = append(out, d)
			}
		}
	})
	return out
}

// A look that came back before the switch is written with the switch, on
// the route that made it (as before).
func TestALookBackBeforeTheSwitchIsCountedOnTheOldRoute(t *testing.T) {
	model := usageModel(t, nil)
	other := usageModel(t, nil)
	rt, _ := netRuntime(t, model.srv.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if o := netWait(t, silentLook(t, rt, ctx)); o.err != nil {
		t.Fatal(o.err)
	}
	switchTo(rt, other.srv.URL, "eagent-other")
	if c := checksEvents(rt); len(c) != 1 || c[0].Asked != testModel || byRoute(rt, model.srv.URL, testModel).Input != 5678 {
		t.Fatalf("checks %+v; by route %+v", c, rt.st.ByRoute)
	}
}

// Two switches while one look is out: it is counted on the route that
// served it, and neither of the others.
func TestALookOutAcrossTwoSwitchesIsCountedOnItsOwnRoute(t *testing.T) {
	release := make(chan struct{})
	model := usageModel(t, release)
	b, c := usageModel(t, nil), usageModel(t, nil)
	rt, _ := netRuntime(t, model.srv.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := silentLook(t, rt, ctx)
	waitFor(t, "the look", func() bool { return model.count(testModel) == 1 })
	switchTo(rt, b.srv.URL, "eagent-b")
	switchTo(rt, c.srv.URL, "eagent-c")
	close(release)
	if o := netWait(t, ch); o.err != nil {
		t.Fatal(o.err)
	}
	rt.sync(rt.flushSilent)
	if a, bu, cu := byRoute(rt, model.srv.URL, testModel).Input, byRoute(rt, b.srv.URL, "eagent-b").Input, byRoute(rt, c.srv.URL, "eagent-c").Input; a != 5678 || bu != 0 || cu != 0 {
		t.Fatalf("by route: a %d, b %d, c %d", a, bu, cu)
	}
}

// The route answers that it cannot serve the model during the look, and
// the fallback serves it: the look is counted on the fallback.
func TestALookServedByAFallbackIsCountedOnTheFallback(t *testing.T) {
	primary := newScripted(func(string, []map[string]any) reply {
		return reply{status: 401, body: `{"error":{"message":"bad key"}}`}
	})
	t.Cleanup(primary.srv.Close)
	fb := usageModel(t, nil)
	rt, _ := netRuntime(t, primary.srv.URL, fb.srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if o := netWait(t, silentLook(t, rt, ctx)); o.err != nil {
		t.Fatal(o.err)
	}
	rt.sync(rt.flushSilent)
	if c := checksEvents(rt); len(c) != 1 || c[0].Asked != "eagent-fallback" || c[0].Host != fb.srv.URL {
		t.Fatalf("checks %+v", c)
	}
	if got := byRoute(rt, fb.srv.URL, "eagent-fallback").Input; got != 100 {
		t.Fatalf("the fallback has %d; by route %+v", got, rt.st.ByRoute)
	}
}

// A batch is written at twenty looks, and a look from another route in the
// middle of a batch starts a new one.
func TestSilentBatchesHoldTwentyLooksOfOneRoute(t *testing.T) {
	model := usageModel(t, nil)
	rt, _ := netRuntime(t, model.srv.URL, "")
	look := func(host, asked string, n int) {
		rt.sync(func() {
			resp := &llm.Response{BaseURL: host, Asked: asked, Usage: event.Usage{Input: n}}
			rt.noteSilent(resp, rt.st.LastSeq(), wake{})
		})
	}
	for range 21 {
		look(model.srv.URL, testModel, 10)
	}
	if c := checksEvents(rt); len(c) != 1 || c[0].Count != 20 || c[0].Usage.Input != 200 || c[0].Asked != testModel {
		t.Fatalf("after 21 looks: %+v", c)
	}
	look("http://late.example", "eagent-late", 7)
	look(model.srv.URL, testModel, 10)
	rt.sync(rt.flushSilent)
	c := checksEvents(rt)
	if len(c) != 4 || c[1].Count != 1 || c[1].Asked != testModel || c[2].Asked != "eagent-late" || c[2].Usage.Input != 7 || c[3].Count != 1 {
		t.Fatalf("batches %+v", c)
	}
}
