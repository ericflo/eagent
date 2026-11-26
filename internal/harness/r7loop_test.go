package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// Finding 21: a reply with no tool call and no words (a provider's error or
// refusal stop) is sent back once like any other reply that reaches no one,
// so the agent's report still reaches the user.
func TestR7AnEmptyReplyIsSentBackOnce(t *testing.T) {
	var mu sync.Mutex
	reminded := 0
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(tail, "had no tool call"):
			reminded++
			return say("Codex merged the PRs.")
		case strings.Contains(tail, "finished a turn"):
			return reply{}
		}
		return hold()
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	mu.Lock()
	defer mu.Unlock()
	if reminded != 1 {
		t.Fatalf("reminded %d times, want 1", reminded)
	}
}

// Finding 24: a closing word that is only a leaked control token says
// nothing, so it is not taken as said: send_message is refused, plain text
// is sent back as a reply with no tool call, and the session does not end
// in silence.
func TestR7AClosingWordOfOnlyControlTokensIsNotTakenAsSaid(t *testing.T) {
	const report = "Final: #42 and #44 are merged; #45 is not."
	t.Run("send_message", func(t *testing.T) {
		s, _, code := audit2Closing(t, func(msgs []map[string]any) reply {
			if strings.Contains(lastText(msgs), "text is required") {
				return say(report)
			}
			return say("<|im_end|>")
		})
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if said := s.ui.said(); len(said) == 0 || said[len(said)-1] != report {
			t.Fatalf("said %q", said)
		}
	})
	t.Run("plain text", func(t *testing.T) {
		s, _, code := audit2Closing(t, func(msgs []map[string]any) reply { return reply{text: "<|im_end|>"} })
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if n := audit2CountLast(s.model, "That reply had no tool call"); n != 1 {
			t.Fatalf("the empty report was sent back %d times, want 1", n)
		}
		// The earlier message was the last thing said before the closing
		// look; something must come after it.
		if said := s.ui.said(); len(said) < 2 {
			t.Fatalf("said %q", said)
		}
	})
}

// r7EffortOnly changes the effort of eagent's model and nothing else, as a
// live settings change does: the same chain, a new client.
func r7EffortOnly(rt *Runtime) {
	rt.sync(func() {
		m := rt.models[event.ActorEagent]
		eps := append([]llm.Endpoint(nil), m.routes...)
		eps[0].ReasoningEffort = "low"
		rt.swapRoute(routeSwap{who: event.ActorEagent, eps: eps, conf: config.ModelRoute{BaseURL: eps[0].BaseURL, Model: eps[0].Model}})
		rt.models[event.ActorEagent].client.MaxAttempts = 1
	})
}

// Finding 22, case 2: the primary was out (the user was told) and answers
// a call during which only the effort changed. The outage is over: the
// route answered, whatever client now serves it.
func TestR7ThePrimaryBackUnderAnEffortChangeEndsTheOutage(t *testing.T) {
	model := netModel(t)
	var answer atomic.Bool
	held, release := make(chan struct{}, 1), make(chan struct{})
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !answer.Load() {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		select {
		case held <- struct{}{}:
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		default:
		}
		model.srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(primary.Close)
	rt, _ := netRuntime(t, primary.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := netCallAsync(rt, ctx)
	waitFor(t, "the outage notice", func() bool { return len(outageNotices(rt)) > 0 })
	answer.Store(true)
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the primary was not asked again")
	}
	r7EffortOnly(rt)
	close(release)
	if o := netWait(t, ch); o.err != nil {
		t.Fatal(o.err)
	}
	var out outage
	rt.sync(func() { out = rt.models[event.ActorEagent].out })
	if out.down() || out.told {
		t.Fatalf("the primary answered, yet the outage stands: %+v", out)
	}
}

// Finding 22, case 3: the primary is dead and a fallback, asked in its
// place past the bound, answers after an effort-only change. It takes over
// as it would without the change, on the chain now configured.
func TestR7AFallbackThatAnswersAfterAnEffortChangeTakesOver(t *testing.T) {
	model := netModel(t)
	held, release := make(chan struct{}, 1), make(chan struct{})
	fb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case held <- struct{}{}:
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		default:
		}
		model.srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(fb.Close)
	rt, _ := netRuntime(t, "http://"+downAddr(t), fb.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := netCallAsync(rt, ctx)
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the fallback was not asked")
	}
	r7EffortOnly(rt)
	close(release)
	if o := netWait(t, ch); o.err != nil || o.resp.Text != "from eagent-fallback" {
		t.Fatalf("call: %v %+v", o.err, o.resp)
	}
	var at int
	var ep llm.Endpoint
	rt.sync(func() { m := rt.models[event.ActorEagent]; at, ep = m.at, m.client.Endpoint })
	if at != 1 || ep.Model != "eagent-fallback" {
		t.Fatalf("the fallback answered past the bound, yet the session is on route %d (%s)", at, ep)
	}
	var routes []event.RouteData
	for _, r := range netRoutes(rt) {
		if strings.Contains(r.Reason, "cannot be reached") {
			routes = append(routes, r)
		}
	}
	if len(routes) != 1 {
		t.Fatalf("takeovers on the record: %+v", routes)
	}
}
