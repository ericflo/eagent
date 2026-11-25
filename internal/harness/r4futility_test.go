package harness

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/tools"
)

// r4Route is a small local route for the tests below: 16,384 tokens with
// 2,048 for the reply, whose provider counts a prompt as mult/10 of
// eagent's estimate (a real tokenizer can: digits, JSON, file:line lists)
// and refuses what is past the window. noUsage: it reports no sizes.
type r4Route struct {
	mult    int
	noUsage bool
	mu      sync.Mutex
	passes  int
}

func (rt *r4Route) brain() func(string, []map[string]any) reply {
	const window, ceiling = 16384, 2048
	b, _ := json.Marshal(tools.All())
	toolTokens := llm.EstimateTokens(string(b))
	return func(_ string, m []map[string]any) reply {
		raw, _ := json.Marshal(m)
		n := (llm.EstimateTokens(string(raw)) + toolTokens) * rt.mult / 10
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if n > window-ceiling {
			return reply{status: 400, body: `{"error":{"message":"prompt is too long: the model's context window is 16384 tokens","type":"invalid_request_error"}}`}
		}
		if ask := dossierRequest(m); ask != "" {
			rt.passes++
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):]), prompt: n, noUsage: rt.noUsage}
		}
		r := hold()
		if strings.Contains(lastUserText(m), "kept for you") && m[len(m)-1]["role"] != "tool" {
			r = say("Noted.")
		}
		r.prompt, r.noUsage = n, rt.noUsage
		return r
	}
}

// r4SmallRoute runs a session on such a route. The user writes msgs
// messages of 1,500 characters, one at a time, each answered. It returns
// the restarts (dossiers recorded) and how many messages came between them.
func r4SmallRoute(t *testing.T, route *r4Route, msgs int, tune func(*config.Config)) (restarts int, between []int, s *session) {
	t.Helper()
	s = startSession(t, brief, route.brain(), func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 16384, 2048
		if tune != nil {
			tune(c)
		}
	})
	t.Cleanup(func() {
		if t.Failed() {
			for _, l := range s.ui.logged() {
				if strings.Contains(l, "restart") || strings.Contains(l, "dossier") || strings.Contains(l, "too long") || strings.Contains(l, "subsession") || strings.Contains(l, "room") {
					t.Log(l)
				}
			}
		}
	})
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	last, since := 0, 0
	for i := 0; i < msgs; i++ {
		before := s.ui.messageCount()
		s.type_(fmt.Sprintf("@e %s", r4Text(i, 1500)))
		s.waitFor(fmt.Sprint("the answer to message ", i), func() bool { return s.ui.messageCount() > before })
		s.settled()
		since++
		if n := len(s.events(event.Dossier)); n > last {
			between = append(between, since)
			last, since = n, 0
		}
	}
	return last, between, s
}

// r4Logged counts the log lines that contain what.
func r4Logged(s *session, what string) int {
	n := 0
	for _, l := range s.ui.logged() {
		if strings.Contains(l, what) {
			n++
		}
	}
	return n
}

// A small route whose provider counts more than eagent's estimate: the
// sizes are held against the provider's own count (learned from the sizes
// it reports, or from its refusals when it reports none), so the session
// settles into restarts that each make room for several messages, never
// one per message, also with rollover_tokens set by hand. Messages of
// 1,500 characters are about a twentieth of what such a route holds past
// eagent's system prompt and tools at 1.5 times the estimate, so a
// restart every message or two would mean the restarts make no room.
func TestSmallRouteRestartsMakeRoomWhenTheProviderCountsMore(t *testing.T) {
	for _, c := range []struct {
		name           string
		rollover, mult int
		noUsage        bool
		most           int // restarts over 24 messages
	}{
		{"automatic, 10% denser", 0, 11, false, 6},
		{"automatic, 20% denser", 0, 12, false, 6},
		{"automatic, 30% denser", 0, 13, false, 8},
		{"rollover_tokens 12000, 30% denser", 12000, 13, false, 8},
		{"automatic, 50% denser", 0, 15, false, 12},
		{"automatic, 30% denser, no sizes reported", 0, 13, true, 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			route := &r4Route{mult: c.mult, noUsage: c.noUsage}
			restarts, between, s := r4SmallRoute(t, route, 24, func(cfg *config.Config) { cfg.RolloverTokens = c.rollover })
			t.Logf("%d restarts over 24 messages, after %v messages", restarts, between)
			if restarts > c.most {
				t.Fatalf("24 messages led to %d restarts (after %v messages), want at most %d", restarts, between, c.most)
			}
			for i, n := range between[1:] {
				if n < 2 {
					t.Fatalf("restart %d came %d message after the one before (%v)", i+2, n, between)
				}
			}
			if n := r4Logged(s, "no restart can make room"); n != 0 {
				t.Fatalf("a route that can hold the conversation was said to have no room (%d times)", n)
			}
		})
	}
}

// A route whose provider counts eagent's system prompt and tools alone past
// what the window takes as input (twice the estimate, here) cannot be
// helped by any restart. The session says so plainly, once, and stops
// restarting instead of looping.
func TestRouteThatCannotHoldTheSystemPromptSaysSoOnceAndStopsRestarting(t *testing.T) {
	route := &r4Route{mult: 20}
	s := startSession(t, brief, route.brain(), func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 16384, 2048
	})
	t.Cleanup(func() {
		if t.Failed() {
			for _, l := range s.ui.logged() {
				t.Log(l)
			}
		}
	})
	s.waitFor("the plain word that no restart can help", func() bool { return r4Logged(s, "no restart can make room") == 1 })
	for i := 0; i < 3; i++ {
		s.type_(fmt.Sprintf("@e %s", r4Text(i, 300)))
	}
	s.waitFor("the messages to be taken in", func() bool { return len(s.events(event.UserMessage)) >= 3 })
	n := len(s.events(event.Dossier))
	// However often the loop looks for a call to make, no restart follows.
	for i := 0; i < 200; i++ {
		s.rt.sync(func() {
			if !s.rt.turns.busy {
				s.rt.maybeTurn()
			}
		})
	}
	s.waitFor("the loop to be idle", func() bool {
		idle := false
		s.rt.sync(func() { idle = !s.rt.turns.busy })
		return idle
	})
	if got := len(s.events(event.Dossier)); got != n || got > 1 {
		t.Fatalf("%d restarts, then %d more", n, got-n)
	}
	if got := r4Logged(s, "no restart can make room"); got != 1 {
		t.Fatalf("said %d times that no restart can help", got)
	}
}
