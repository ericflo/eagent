package harness

import (
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

// The first turn's first call comes back and its next one fails; the turn
// after it fails on its first call. A call of the run came back, in the
// turn before: the user is told the calls keep failing, not that the
// model cannot be reached. Two failures with no call back between them
// are still told as the model out of reach.
func TestK2TheFailingNoticeCountsACallThatCameBackInAnEarlierTurn(t *testing.T) {
	for _, c := range []struct {
		name   string
		lookup bool // the first turn's first call comes back
		want   string
	}{
		{"a call came back", true, failingNotice},
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
