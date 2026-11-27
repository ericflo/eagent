package harness

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// k1BadRequest is a failure that is not a refusal as too long, and that
// the client does not retry on its own.
var k1BadRequest = reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}

// Every turn's first call goes through (it makes a lookup) and the call
// after it fails, not as too long. The turn that failed is owed again, so
// the next one starts; its first call coming back is no sign that the
// turn's calls go through, so the waits between those turns grow as for
// any failing call, where each turn once started the next at once, paid
// for its first call, and said it was trying again now, without end.
func TestK1ASecondCallThatFailsAfterTheFirstWentThroughWaitsLongerEachTime(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var ready atomic.Bool
	brain := func(_ string, m []map[string]any) reply {
		switch {
		case !ready.Load():
			return hold()
		case m[len(m)-1]["role"] == "tool":
			return k1BadRequest
		}
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
	}
	s := startSession(t, "", brain, nil)
	r5LogOnFailure(s)
	s.settled()
	ready.Store(true)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	r6Until(t, "a wait of an hour, or calls going on without one", func() bool {
		return r4Logged(s, "trying again in") > 0 || r4Logged(s, "model call failed") > 30
	})
	if n := r4Logged(s, "trying again in"); n != 1 {
		t.Fatalf("a second call that failed after the first went through was tried again at once each time, the waits never growing (%d failed calls logged)", r4Logged(s, "model call failed"))
	}
	if n := r4Logged(s, "model call failed"); n != 2 {
		t.Fatalf("%d failed calls logged before the wait of an hour, want 2", n)
	}
}

// A session with no terminal whose restart could not be made (the file
// for the next conversation could not be created) has its prompt refused
// as too long twice more: two failures in a row. Then a turn of one call
// runs to its end: the model answers. That turn shows the model can be
// reached, so the session ends with its closing word, not as one whose
// model is down (eagent's own closing words, exit 2).
func TestK1ABatchSessionWhoseTurnEndedWellAfterRefusalsIsNotEndedAsDown(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made read-only for root")
	}
	setFor(t, &errorRetryDelays, []time.Duration{0, 0, time.Hour})
	t.Setenv("EAGENT_TEST_KEY", "x")
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	var writing atomic.Bool
	var calls atomic.Int32
	model := newScripted(func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			writing.Store(true)
			p := places(ask)
			return reply{block: gate, text: briefing(p[max(0, len(p)-3):])}
		}
		// The first turn stops the agent, and the call after that is
		// refused as too long: a restart, which cannot be made. Two more
		// refusals, then a turn of one call that answers.
		switch n := calls.Add(1); {
		case n == 1:
			return stopsAndReports("Stopping Codex; checking the status.")
		case n <= 4:
			return r7TooLong
		case m[len(m)-1]["role"] == "tool":
			return hold()
		}
		return say("The status is green.")
	})
	t.Cleanup(model.srv.Close)
	useFakeRelays(t, newFakeSeat("ok"))
	ui := &fakeUI{}
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: "@e what is the status?"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- rt.Run(context.Background()) }()
	waitFor(t, "the dossier call to go out", writing.Load)
	if err := os.Chmod(rt.sess.Path, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(rt.sess.Path, 0o755) })
	release()
	var code int
	select {
	case code = <-done:
	case <-time.After(15 * time.Second):
		rt.stop()
		<-done
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	failed, gaveUp := 0, 0
	for _, l := range ui.logged() {
		if strings.Contains(l, "model call failed") {
			failed++
		}
		if strings.Contains(l, "the conversation continues as it is for now") {
			gaveUp++
		}
	}
	if failed != 2 || gaveUp != 1 {
		t.Fatalf("%d failed calls and %d restarts given up, want 2 and 1", failed, gaveUp)
	}
	for _, m := range ui.said() {
		if strings.HasPrefix(m, "This session is ending and I could not write a proper closing report") {
			t.Fatalf("a session whose last turn ended well was closed as one whose model is down: %q", m)
		}
	}
	if code != 0 {
		t.Fatalf("exit %d after a turn that ended well, want 0; said %q", code, ui.said())
	}
}

// k1Swap moves eagent's calls to another model with this window and reply
// ceiling, served by brain, as a pick in the thread's settings does.
func k1Swap(t *testing.T, s *session, window, ceiling int, brain func(string, []map[string]any) reply) *scripted {
	srv := newScripted(brain)
	t.Cleanup(srv.srv.Close)
	conf := config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: srv.srv.URL, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: window, MaxTokens: ceiling}
	eps, err := conf.Routes()
	if err != nil {
		t.Fatal(err)
	}
	s.rt.sync(func() {
		s.rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: "other-model"}, eps: eps, conf: conf})
	})
	return srv
}

// The burst of pictures keeps coming during both passes on the first
// model, so the one more try is spent and no restart can make room there.
// The person then picks a model with the same room: the restarts in a row
// and the one more try were the old model's (newModel), so the new one
// gets its own restart, which sets the burst aside by place, and the
// person's message is answered. The decision found on the old model does
// not stand on it, as one found on the opening alone would.
func TestK1ABurstDecisionDoesNotStandOnAModelPickedAfterIt(t *testing.T) {
	const window, ceiling = 128_000, 16384
	t.Setenv("EAGENT_TEST_KEY", "x")
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	s, _ := r6QuietRestart(t, window, func(s *session, pass int) {
		if pass <= 2 {
			r6SendPictures(s, window)
		}
	})
	if n := r5Told(s, fullNotice+": while I was rereading"); n != 1 {
		t.Fatalf("told %d times that what arrived during the passes is too much, want once", n)
	}
	before := r6Dossiers(s)
	k1Swap(t, s, window, ceiling, r6Brain(window, ceiling, 100))
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "@e still there?", Route: "eagent"}) })
	r6Until(t, "an answer, or the message told it waits", func() bool {
		return r5Told(s, "Noted.") > 1 || r5Told(s, stillFullNotice+`"@e still there?"`) > 0 || r5Told(s, stillFullNotice+`"still there?"`) > 0
	})
	if r5Told(s, "Noted.") < 2 {
		t.Fatalf("on the model picked after the decision: %d restarts, and the message was told it waits; that model's own restart sets the burst aside", r6Dossiers(s)-before)
	}
	if n := r6Dossiers(s) - before; n != 1 {
		t.Fatalf("%d restarts on the model picked after the decision, want its one", n)
	}
}
