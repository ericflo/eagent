package harness

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// Each turn's first call comes back; the call after it fails (400). The
// model is reached on every turn, yet after two such turns the user is
// told eagent cannot reach its own model.
func TestRV15UnreachableNoticeWhileTheModelAnswersEachTurn(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var ready atomic.Bool
	var answered atomic.Int32
	brain := func(_ string, m []map[string]any) reply {
		switch {
		case !ready.Load():
			return hold()
		case m[len(m)-1]["role"] == "tool":
			return k1BadRequest
		}
		answered.Add(1)
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
	}
	s := startSession(t, "", brain, nil)
	r5LogOnFailure(s)
	s.settled()
	ready.Store(true)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	r6Until(t, "a wait of an hour", func() bool {
		// The wait is logged before the notice is written: wait for both.
		return r4Logged(s, "trying again in") > 0 && s.ui.messageCount() > 0 || r4Logged(s, "model call failed") > 30
	})
	var told []string
	s.rt.sync(func() {
		for _, ev := range s.rt.st.Events {
			var d event.MessageData
			if ev.Type == event.Message && ev.Decode(&d) == nil && d.Auto && strings.Contains(d.Text, "can't reach my own model") {
				told = append(told, d.Text)
			}
		}
	})
	if len(told) > 0 {
		t.Fatalf("the model answered %d calls (one each turn), and the user was told: %q", answered.Load(), told[0])
	}
	if n := r5Told(s, failingNotice); n != 1 {
		t.Fatalf("told %d times that the calls keep failing, want once", n)
	}
}

// The first turn's first call comes back, or not, and its next one fails;
// the turn after it fails on its first call. Since that first failure
// nothing came back, so in both cases the user is told the model refuses
// its calls (each a 400), not that they keep failing after one went
// through: the notice goes by the turn that failed second, as
// TestRV15UnreachableNoticeWhileTheModelAnswersEachTurn has it when that
// turn's first call came back.
func TestK2TheFailingNoticeGoesByTheTurnThatFailedSecond(t *testing.T) {
	for _, c := range []struct {
		name   string
		lookup bool // the first turn's first call comes back
		want   string
	}{
		{"one came back before the first failure", true, "My model refuses my calls"},
		{"none came back", false, "My model refuses my calls"},
	} {
		t.Run(c.name, func(t *testing.T) {
			setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
			var ready atomic.Bool
			var calls atomic.Int32
			brain := func(_ string, m []map[string]any) reply {
				if !ready.Load() {
					return hold()
				}
				if calls.Add(1) == 1 && c.lookup {
					return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
				}
				return k1BadRequest
			}
			s := startSession(t, "", brain, nil)
			r5LogOnFailure(s)
			s.settled()
			ready.Store(true)
			s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
			// The wait is logged before the notice is written: wait for both.
			r6Until(t, "a wait of an hour and the notice", func() bool {
				return r4Logged(s, "trying again in") > 0 && s.ui.messageCount() > 0
			})
			told := 0
			for _, m := range s.ui.said() {
				if strings.Contains(m, "keep failing") || strings.Contains(m, "can't reach my own model") || strings.Contains(m, "refuses my calls") {
					told++
					if !strings.HasPrefix(m, c.want) {
						t.Fatalf("told %q, want %q", m, c.want)
					}
				}
			}
			if told != 1 {
				t.Fatalf("told %d times after two failures, want once", told)
			}
		})
	}
}

// k2Batch starts a session with no terminal on brain and returns it with
// its UI and a channel that gets its exit code.
func k2Batch(t *testing.T, prompt string, brain func(string, []map[string]any) reply) (*Runtime, *fakeUI, chan int) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	model := newScripted(brain)
	t.Cleanup(model.srv.Close)
	useFakeRelays(t, newFakeSeat("ok"))
	ui := &fakeUI{}
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: prompt}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	})
	return rt, ui, done
}

// A session with no terminal stops its agent, and from then on every
// turn's first call comes back and the call after it fails (400). Nothing
// keeps it open, so after two failed looks in a row it ends as down (exit
// 2) rather than pay for a look an hour for ever. Its words are true for
// it: its calls kept failing partway, not a model that cannot be reached,
// and nothing promises a retry it will not make.
//
// This test first held (RV15) that such a session is not ended as one
// whose model is down while the model answers each turn: then it would
// have closed in words that called a model which answers unreachable. The
// words still may not; the ending is the coordinator's rule of the sixth
// self-review.
func TestK2ABatchSessionWhoseModelAnswersEachTurnEndsAsDownInTrueWords(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var calls atomic.Int32
	_, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		switch {
		case m[len(m)-1]["role"] == "tool":
			return k1BadRequest
		case calls.Add(1) == 1:
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"the work is done"}`)}}
		}
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
	})
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	var closing string
	for _, m := range ui.said() {
		if strings.HasPrefix(m, failingNotice) || strings.Contains(m, "I keep trying") || strings.Contains(m, "can't reach") {
			t.Fatalf("told %q, and the session ended", m)
		}
		if strings.HasPrefix(m, "This session is ending and I could not write a proper closing report") {
			closing = m
		}
	}
	if code != 2 || !strings.Contains(closing, "kept failing partway through my looks") {
		t.Fatalf("exit %d, closing words %q; want exit 2 and words that say its calls kept failing partway", code, closing)
	}
}

// The same failures in a session something keeps open (its agent still
// runs): it keeps trying after the wait, and tells the user its calls keep
// failing, which is true, and that it keeps trying, which it does.
func TestK2AKeptOpenBatchSessionWhoseModelAnswersEachTurnKeepsTrying(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	_, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if m[len(m)-1]["role"] == "tool" {
			return k1BadRequest
		}
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
	})
	r6Until(t, "two failed calls told", func() bool {
		for _, m := range ui.said() {
			if strings.HasPrefix(m, failingNotice) {
				return true
			}
		}
		select {
		case code := <-done:
			done <- code
			return true
		default:
		}
		return false
	})
	select {
	case code := <-done:
		done <- code
		t.Fatalf("the session ended (exit %d) while its agent ran; said %q", code, ui.said())
	case <-time.After(10 * loopPoll):
	}
	for _, m := range ui.said() {
		if strings.HasPrefix(m, failingNotice) && !strings.Contains(m, "I keep trying") {
			t.Fatalf("told %q", m)
		}
		if strings.HasPrefix(m, "This session is ending") {
			t.Fatalf("closed while its agent ran: %q", m)
		}
	}
}

// A session with no terminal fails its first two calls (the user is told
// the model cannot be reached). The model then answers: it stops the
// agent, and the call after that is refused as too long, which restarts
// the conversation from a dossier and counts no failure. The model
// answered, so the two failures before are over: the first call after the
// restart fails, is tried again at once as a first failure is, and the
// session ends with its closing word (exit 0), not as one whose model is
// down after a wait of an hour.
func TestK2ARestartAfterAnAnsweredCallEndsTheFailuresBeforeIt(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, 0, time.Hour})
	setFor(t, &wakeSettle, 100*time.Millisecond)
	var calls atomic.Int32
	_, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch n := calls.Add(1); {
		case n <= 2, n == 5:
			return k1BadRequest
		case n == 3:
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"the work is done"}`)}}
		case n == 4:
			return r7TooLong
		case m[len(m)-1]["role"] == "tool", !strings.Contains(lastUserText(m), "The session is ending now"):
			return hold()
		}
		return say("The build is green.")
	})
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatalf("the session did not end; said %q; logs %q", ui.said(), ui.logged())
	}
	logged := strings.Join(ui.logged(), "\n")
	if !strings.Contains(logged, "begins with a dossier") {
		t.Fatalf("setup: no restart was made; logs %q", ui.logged())
	}
	if strings.Contains(logged, "trying again in") {
		t.Fatalf("a failure after the restart waited as the third in a row: %q", ui.logged())
	}
	if code != 0 {
		t.Fatalf("exit %d, want 0; said %q", code, ui.said())
	}
}
