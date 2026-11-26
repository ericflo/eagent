package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/relay"
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

// r4Until is waitFor polling every millisecond: the r4 sessions below wait
// on the loop dozens of times each, so the poll interval is most of their
// time.
func r4Until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// r4Settled is session.settled polling every millisecond: it waits until
// the loop has nothing in flight and nothing owed a look, seen so on eight
// looks in a row. Every message the loop takes runs its scheduler before
// the next look is served (Run), so a look that finds it idle comes after
// whatever that message started; the eight are for a margin, as in
// settled.
func r4Settled(s *session) {
	s.t.Helper()
	quiet := 0
	r4Until(s.t, "the loop to settle", func() bool {
		idle := false
		s.rt.sync(func() { idle = !s.rt.turns.busy && !s.rt.wakePending() && len(s.rt.later) == 0 })
		if idle {
			quiet++
		} else {
			quiet = 0
		}
		return quiet >= 8
	})
}

// r4ParallelSessions lets the subtests of t each run a session of
// r4StartSession in parallel (t.Parallel): what startSession sets for the
// whole process (the test key, the hooks that start an agent) is set once
// here, on the parent, and every session's agent gets a fake seat of its
// own, found by the directory it runs in, which is the session's project.
// The subtests must touch no other package-level state.
func r4ParallelSessions(t *testing.T) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	var mu sync.Mutex
	seats := map[string]*fakeSeat{}
	seat := func(dir string) *fakeSeat {
		mu.Lock()
		defer mu.Unlock()
		if seats[dir] == nil {
			seats[dir] = newFakeSeat("Merged #42 and #44; main is green at c92b5bad.")
		}
		return seats[dir]
	}
	oldLaunch, oldAttach := launchRelay, attachRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		return seat(spec.Dir), relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	attachRelay = func(spec relay.Spec, host, target, innerID, transcript string, since time.Time) (relaySeat, error) {
		return seat(spec.Dir), nil
	}
	t.Cleanup(func() { launchRelay, attachRelay = oldLaunch, oldAttach })
}

// r4StartSession is startSession for a subtest of r4ParallelSessions: the
// same session, without setting what the parent set.
func r4StartSession(t *testing.T, prompt string, brain func(model string, msgs []map[string]any) reply, tune func(*config.Config)) *session {
	t.Helper()
	model := newScripted(brain)
	t.Cleanup(model.srv.Close)
	cfg := testConfig(model.srv.URL)
	if tune != nil {
		tune(&cfg)
	}
	ui := &fakeUI{input: make(chan string, 16)}
	rt, err := New(cfg, Options{Project: t.TempDir(), Interactive: true, Prompt: prompt}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s := &session{t: t, rt: rt, ui: ui, model: model, cancel: cancel, done: make(chan int, 1)}
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	return s
}

// r4SmallRoute runs a session on such a route (in a subtest of
// r4ParallelSessions). The user writes msgs messages of 1,500 characters,
// one at a time, each answered. It returns the restarts (dossiers
// recorded) and how many messages came between them.
func r4SmallRoute(t *testing.T, route *r4Route, msgs int, tune func(*config.Config)) (restarts int, between []int, s *session) {
	t.Helper()
	s = r4StartSession(t, brief, route.brain(), func(c *config.Config) {
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
	r4Until(t, "the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	r4Settled(s)
	last, since := 0, 0
	for i := 0; i < msgs; i++ {
		before := s.ui.messageCount()
		s.type_(fmt.Sprintf("@e %s", r4Text(i, 1500)))
		r4Until(t, fmt.Sprint("the answer to message ", i), func() bool { return s.ui.messageCount() > before })
		r4Settled(s)
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
// Sixteen messages give every case at least two restarts; a session that
// held its sizes to the estimate alone restarts about every second
// message from 30% denser on (seven times or more in sixteen).
//
// The cases run in parallel, each with a session and an agent of its own.
func TestSmallRouteRestartsMakeRoomWhenTheProviderCountsMore(t *testing.T) {
	r4ParallelSessions(t)
	const msgs = 16
	for _, c := range []struct {
		name           string
		rollover, mult int
		noUsage        bool
		most           int // restarts over the 16 messages
	}{
		{"automatic, 10% denser", 0, 11, false, 4},
		{"automatic, 20% denser", 0, 12, false, 4},
		{"automatic, 30% denser", 0, 13, false, 5},
		{"rollover_tokens 12000, 30% denser", 12000, 13, false, 5},
		{"automatic, 50% denser", 0, 15, false, 8},
		{"automatic, 30% denser, no sizes reported", 0, 13, true, 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			route := &r4Route{mult: c.mult, noUsage: c.noUsage}
			restarts, between, s := r4SmallRoute(t, route, msgs, func(cfg *config.Config) { cfg.RolloverTokens = c.rollover })
			t.Logf("%d restarts over %d messages, after %v messages", restarts, msgs, between)
			if restarts > c.most {
				t.Fatalf("%d messages led to %d restarts (after %v messages), want at most %d", msgs, restarts, between, c.most)
			}
			if restarts < 2 {
				t.Fatalf("%d messages led to %d restarts: too few to see the room each makes", msgs, restarts)
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
