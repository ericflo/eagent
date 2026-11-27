package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// The sibling of cd6c922 (C5) that it left: a session with no terminal
// stops its agent and reports, and that last ordinary turn (not the
// closing look) puts the conversation past the restart size. At the quiet
// moment right after it, maybeTurn runs before maybeEnd in the same tick:
// the session is not yet "ending" (stillClosing: no closing word asked),
// so the quiet-moment restart is made, and a dossier pass is paid for
// before a session that nothing keeps open asks its closing word and ends.
// Known limit (seventh review's notes): "a batch session that is ending can
// pay for a dossier pass it does not need".
func TestSR5LoopABatchSessionAboutToEndPaysForADossier(t *testing.T) {
	var passes, closing atomic.Int32
	_, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			passes.Add(1)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case strings.Contains(lastUserText(m), "The session is ending now"):
			closing.Add(1)
			return hold()
		case m[len(m)-1]["role"] == "tool" || strings.Contains(lastUserText(m), "Stopped Codex"):
			// The last call of the turn that stopped the agent and reported:
			// past the 20,000 restart size of testConfig, well inside the
			// window.
			r := hold()
			r.prompt = 30000
			return r
		}
		return stopsAndReports("The build is green; stopping Codex.")
	})
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	if code != 0 || closing.Load() == 0 {
		t.Fatalf("setup: exit %d, %d closing looks; said %q", code, closing.Load(), ui.said())
	}
	if n := passes.Load(); n != 0 {
		t.Fatalf("%d dossier pass(es) paid for by a session with nothing to keep it open, only to ask its closing word on the new conversation (the old one fit)", n)
	}
}

// k7RateLimited is a provider's 429: a status the llm client takes as
// transient (Retryable), not a refusal.
var k7RateLimited = reply{status: 429, body: `{"error":{"message":"rate limit reached","type":"rate_limit_error"}}`}

// 0651b1e made any 4xx final for a session that is ending, a rate limit
// with the rest: an ending batch session whose model kept answering 429 to
// a notice's look ended as done (exit 0). A rate limit is the model not
// answering for now: the look is tried again after its wait, and a
// session whose calls all fail ends as one whose model could not be
// reached (awaiting-input, exit 2; DESIGN.md).
func TestK7AnEndingSessionRateLimitedEndsAsDown(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var after atomic.Int32
	var rt atomic.Pointer[Runtime]
	var injected, stopped atomic.Bool
	model := audit2Model(t, func(_ string, m []map[string]any) reply {
		switch {
		case injected.Load():
			after.Add(1)
			return k7RateLimited
		case m[len(m)-1]["role"] == "tool":
			return hold()
		case strings.Contains(lastUserText(m), "The session is ending now"):
			if r := rt.Load(); r != nil && !injected.Load() {
				r.sync(func() {
					r.append(event.New(event.HarnessMessage, event.ActorEagent, event.HarnessMessageData{Text: "[eagent] A note of your own came due."}))
				})
				injected.Store(true)
			}
			return hold()
		case stopped.CompareAndSwap(false, true):
			return stopsAndReports("The build is green; stopping Codex.")
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: "@e check the build"}, newFakeSeat("ok"), model, func(r *Runtime) {
		r.models[event.ActorEagent].client.MaxAttempts = 1 // no 5 s waits of the client's own
		rt.Store(r)
	})
	code := coreExit(s)
	if !injected.Load() {
		t.Fatalf("setup: no notice; exit %d", code)
	}
	if n := after.Load(); code != 2 || n != 2 {
		t.Fatalf("exit %d after %d rate-limited calls; want exit 2 after 2 (tried again, then down)", code, n)
	}
}
