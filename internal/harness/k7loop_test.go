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

// The closing word of a batch session is refused (400), which is final
// for a session that is ending, and during its last title and summary
// (refused too) a timer keeps the session open: it stops ending, and no
// call comes back. When the timer fires, the session is ending again, and
// the refusal kept from before (closingRefused) ended it at once: the
// timer's notice never got its look. The refusal was final for the closing
// it came in; the notice is looked at once the retry's wait is over.
func TestK7ARefusalFromAClosingThatStoppedDoesNotEndTheNextOne(t *testing.T) {
	coreSummariesOn(t, false)
	setFor(t, &errorRetryDelays, []time.Duration{300 * time.Millisecond, time.Hour})
	var phase atomic.Int32
	var fired atomic.Bool
	var rt atomic.Pointer[Runtime]
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		summary := strings.Contains(tail, "this look is only about the thread's title and summary")
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			return k1BadRequest
		case summary && phase.CompareAndSwap(2, 3):
			r := rt.Load()
			r.sync(func() {
				r.append(event.New(event.ScheduleCreate, event.ActorEagent, event.ScheduleCreateData{ID: "s1", Kind: "once", Spec: "1h", Note: "check the build again", Next: time.Now().Add(time.Hour)}))
			})
			return k1BadRequest
		case fired.Load() && phase.CompareAndSwap(3, 4):
			return hold() // the timer's look
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsAndReports("Done: the build is green.")
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, newFakeSeat("The build is green."), model, func(r *Runtime) { rt.Store(r) })
	r6Until(t, "the last summary's refusal", func() bool {
		for _, l := range s.ui.logged() {
			if strings.Contains(l, "model call failed") {
				return true
			}
		}
		return false
	})
	r := rt.Load()
	r.sync(func() {
		r.append(event.New(event.ScheduleFire, event.ActorHarness, event.ScheduleFireData{ID: "s1", Note: "check the build again"}))
	})
	fired.Store(true)
	code := coreExit(s)
	if p := phase.Load(); p != 4 || code != 0 {
		t.Fatalf("phase %d, exit %d: the timer's notice got no look; want its look (phase 4) and exit 0; logged %q", p, code, s.ui.logged())
	}
}

// An ending batch session whose notice's look gets its first call answered
// (a lookup) and its next one rate-limited, look after look. Each failed
// look came back first, so the session was never down: it told the user
// it keeps trying, and paid for a look on every retry (up to one an hour)
// for as long as the process lived. An ending session ends as down once
// its look has failed twice in a row, whatever came back before the
// failure, and says nothing that promises a retry.
func TestK7AnEndingSessionWhoseLooksFailAfterALookupEndsAsDown(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Millisecond})
	var after atomic.Int32
	var rt atomic.Pointer[Runtime]
	var injected, stopped atomic.Bool
	model := audit2Model(t, func(_ string, m []map[string]any) reply {
		switch {
		case injected.Load():
			after.Add(1)
			if m[len(m)-1]["role"] == "tool" {
				return k7RateLimited
			}
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
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
	r6Until(t, "the session to end, or forty calls after the notice", func() bool {
		select {
		case code := <-s.done:
			s.done <- code
			return true
		default:
		}
		return after.Load() >= 40
	})
	var code int
	select {
	case code = <-s.done:
		s.done <- code
	default:
		t.Fatalf("an ending session made %d calls after the notice, every other one rate-limited, and still runs; said %q", after.Load(), s.ui.said())
	}
	if n := after.Load(); code != 2 || n != 4 {
		t.Fatalf("exit %d after %d calls; want exit 2 after the two looks (4 calls)", code, n)
	}
	for _, m := range s.ui.said() {
		if strings.Contains(m, "I keep trying") {
			t.Fatalf("told %q, and the session ended", m)
		}
	}
}
