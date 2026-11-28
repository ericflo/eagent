package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// A session with no terminal is ending, and its closing call is out on
// one model when the user picks another. The call, made on the old model,
// then fails. That says nothing of the model in use: the closing word is
// asked of it, and the user is not told that no closing report could be
// written.
func TestAClosingCallThatFailsAfterAModelChangeIsAskedOfTheNewModel(t *testing.T) {
	closing := make(chan struct{})
	release := make(chan struct{})
	var reported, asked atomic.Bool
	rt, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		switch {
		case strings.Contains(lastUserText(m), "The session is ending now"):
			if !asked.Swap(true) {
				close(closing)
			}
			return reply{block: release, status: 400, body: `{"error":{"message":"the request was refused","type":"invalid_request_error"}}`}
		case m[len(m)-1]["role"] == "tool" || reported.Swap(true):
			return hold()
		}
		return stopsAndReports("The build is green; stopping Codex.")
	})
	other := newScripted(func(_ string, m []map[string]any) reply {
		if m[len(m)-1]["role"] == "tool" {
			return hold()
		}
		return say("Closing: the build is green and Codex is stopped.")
	})
	t.Cleanup(other.srv.Close)
	select {
	case <-closing:
	case <-time.After(10 * time.Second):
		t.Fatal("setup: the closing call was never made")
	}
	conf := config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: other.srv.URL, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 60000, MaxTokens: 1024}
	eps, err := conf.Routes()
	if err != nil {
		t.Fatal(err)
	}
	rt.sync(func() {
		rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: "other-model"}, eps: eps, conf: conf})
	})
	close(release)
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatal("the session did not end")
	}
	told := strings.Join(ui.said(), "\n---\n")
	if n := other.count("other-model"); n == 0 || strings.Contains(told, "could not write a proper closing report") || !strings.Contains(told, "Closing: the build is green") {
		t.Fatalf("exit %d: the model picked while the closing call was out got %d calls; the user was told:\n%s", code, n, told)
	}
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
}

// The route answers that it cannot serve the model, the call goes on to
// the fallback within the same look, and the fallback refuses it. That
// failure is the fallback's own: it is counted and written down, and is
// not taken for a call made before the model changed (which is tried
// again at once, uncounted, for as long as it fails).
func TestAFallbacksOwnFailureIsCountedAgainstIt(t *testing.T) {
	fb := newScripted(func(string, []map[string]any) reply {
		return reply{status: 400, body: `{"error":{"message":"the request was refused","type":"invalid_request_error"}}`}
	})
	t.Cleanup(fb.srv.Close)
	s := startSession(t, "", func(string, []map[string]any) reply {
		return reply{status: 401, body: `{"error":{"message":"bad key"}}`}
	}, func(c *config.Config) {
		c.Model.Fallback = &config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: fb.srv.URL, Model: "eagent-fallback", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 60000, MaxTokens: 1024}
	})
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "hello", Route: "eagent"}) })
	r4Until(t, "the fallback's failure on the record", func() bool { return len(s.events(event.Error)) > 0 })
	for _, l := range s.ui.logged() {
		if strings.Contains(l, "before the model changed") {
			t.Fatalf("the fallback's own failure was taken for an older model's: %s", l)
		}
	}
}

// A question too long for a card is in the FinaleChat thread as a
// message, and the user types /dismiss there, from an app that says
// nothing of itself (no client details come with the message). The
// dismissal came from the thread: the thread is not told the question
// "was dismissed elsewhere".
func TestADismissTypedInTheThreadIsNotCalledDismissedElsewhere(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r8AskSession(t, fp, "Which branch?", r8Choices(21))
	waitFor(t, "the question in the thread", func() bool { return k1AskedAsMessage(fp, "Which branch?") == 1 })
	r14Drained(t, g)
	g.rt.sync(func() { g.rt.onInputFrom("/dismiss", "finalechat", "m9", "", nil) })
	waitFor(t, "the dismissal on the record", func() bool { return len(g.events(event.UserDismiss)) == 1 })
	r14Drained(t, g)
	var d event.UserDismissData
	_ = g.events(event.UserDismiss)[0].Decode(&d)
	if d.Source != "finalechat" || k1Settled(fp) != 0 {
		t.Fatalf("dismissed from %q, and the thread got %d notes that the question was dismissed elsewhere", d.Source, k1Settled(fp))
	}
}
