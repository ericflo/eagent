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
		return r4Logged(s, "trying again in") > 0 || r4Logged(s, "model call failed") > 30
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
// nothing came back, so in both cases the user is told the model cannot
// be reached: the notice goes by the turn that failed second, as
// TestRV15UnreachableNoticeWhileTheModelAnswersEachTurn has it when that
// turn's first call came back.
func TestK2TheFailingNoticeGoesByTheTurnThatFailedSecond(t *testing.T) {
	for _, c := range []struct {
		name   string
		lookup bool // the first turn's first call comes back
		want   string
	}{
		{"one came back before the first failure", true, "I can't reach my own model right now"},
		{"none came back", false, "I can't reach my own model right now"},
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
			r6Until(t, "a wait of an hour", func() bool { return r4Logged(s, "trying again in") > 0 })
			told := 0
			for _, m := range s.ui.said() {
				if strings.Contains(m, "keep failing") || strings.Contains(m, "can't reach my own model") {
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
// turn's first call comes back and the call after it fails (400). The
// model answers each turn, so the session is not one whose model is down:
// it does not end in eagent's own closing words with exit 2, but tries
// again after the wait, as a session with an agent would.
func TestK2ABatchSessionWhoseModelAnswersEachTurnIsNotEndedAsDown(t *testing.T) {
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
		t.Fatalf("the session ended (exit %d) while its model answered each turn; said %q", code, ui.said())
	case <-time.After(10 * loopPoll):
	}
	for _, m := range ui.said() {
		if strings.HasPrefix(m, "This session is ending and I could not write a proper closing report") {
			t.Fatalf("closed as one whose model is down: %q", m)
		}
	}
}
