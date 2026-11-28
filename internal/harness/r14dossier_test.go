package harness

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// r14AgentTurn records that the coding agent finished a turn: something
// that asks eagent for a look.
func r14AgentTurn(s *session, text string) {
	s.rt.sync(func() {
		s.rt.append(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "codex", Text: text}))
	})
}

// A working agent records progress while every dossier is written. Each
// new conversation's first look holds (it leaves only its batch,
// event.Checks), and the agent ends a turn meanwhile; the look at that
// makes a lookup, and the call after it is refused as too long. A hold
// adds nothing to the conversation and no turn of it ran to its end, so
// each of those restarts counts in the row: they stop at the bound and the
// user is told, where the hold once started the count again after every
// restart.
func TestR14HoldBeforeALookupThatOverflowsIsNotATurnThatEnded(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	var s *session
	var ready atomic.Bool
	var looks atomic.Int32 // looks since the last dossier
	brain := func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			looks.Store(0)
			if ready.Load() {
				s.rt.sync(func() {
					s.rt.append(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "codex", Text: "running the tests"}))
				})
			}
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case !ready.Load():
			return hold()
		case m[len(m)-1]["role"] == "tool":
			return r7TooLong
		case looks.Add(1) == 1:
			r14AgentTurn(s, "The unit tests pass.")
			return hold()
		}
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
	}
	s = startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 60000, 2048
	})
	r5LogOnFailure(s)
	s.settled()
	ready.Store(true)
	r14AgentTurn(s, "The build is fixed.")
	r6Until(t, "the user told the conversation is full, or too many restarts", func() bool {
		return r6ToldFull(s) > 0 || r6Dossiers(s) > restartRescueMax+4
	})
	if n := r6Dossiers(s); n > restartRescueMax+2 {
		t.Fatalf("%d restarts in a row, each after a hold and a lookup refused as too long; want at most %d", n, restartRescueMax+2)
	}
	if n := r5Told(s, fullNotice+": after several restarts in a row"); n != 1 {
		t.Fatalf("told %d times that the restarts stopped as the window was reached again, want once", n)
	}
}

// Once the restarts in a row have stopped, every turn's first call still
// goes through (it makes a lookup) and the call after it is refused as too
// long. No restart is made for that refusal, so it is a failed call like
// any other: the turns after it wait, longer and longer, where each once
// started the next at once, without end, and said it was restarting.
func TestR14AfterTheRestartsStopARefusedSecondCallWaitsLongerEachTime(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var s *session
	var ready atomic.Bool
	brain := func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			if ready.Load() {
				s.rt.sync(func() {
					s.rt.append(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "codex", Text: "running the tests"}))
				})
			}
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case !ready.Load():
			return hold()
		case m[len(m)-1]["role"] == "tool":
			return r7TooLong
		}
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
	}
	s = startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 60000, 2048
	})
	r5LogOnFailure(s)
	s.settled()
	ready.Store(true)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	r6Until(t, "a wait of an hour, or calls going on without one", func() bool {
		return r4Logged(s, "trying again in") > 0 || r4Logged(s, "refused the prompt as too long")+r4Logged(s, "model call failed") > 30
	})
	if n := r5Told(s, fullNotice+": after several restarts in a row"); n != 1 {
		t.Fatalf("told %d times that the restarts stopped as the window was reached again, want once", n)
	}
	if n := r4Logged(s, "trying again in"); n != 1 {
		t.Fatalf("after the restarts stopped, the refused calls went on without the waits between them growing (%d refusals, %d failed calls logged)", r4Logged(s, "refused the prompt as too long"), r4Logged(s, "model call failed"))
	}
	if said, made := r4Logged(s, "restarting the conversation from a dossier"), r6Dossiers(s); said != made {
		t.Fatalf("said %d times that the conversation is restarted from a dossier, and %d restarts were made", said, made)
	}
}

// Two new conversations in a row are past the restart size on their first
// call, and the third is refused as too long after its lookup: that one
// is restarted too, one past the bound on restarts in a row. The fourth is
// past the restart size again, and the restarts stop. What the user is
// told then about the window must hold when the window is reached: no
// restart is made there, so none is promised, and the refusal there is
// not said to come after a restart that was never made.
func TestR14NoRestartAtTheWindowIsPromisedWhenNoneWillBeMade(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	const (
		holding = iota
		past    // every call is past the restart size
		lookup  // a lookup, and the call after it refused as too long
	)
	var phase atomic.Int32
	var limit, refusals atomic.Int64
	var s *session
	// send records a message for eagent with a picture: a request that
	// sends one teaches no ratio (calibrate), neither from the sizes
	// reported nor from a refusal, so the sizes stay those the provider
	// reports.
	send := func(i int) {
		d := r6WithPictures(fmt.Sprintf("status %d?", i), 1, s)
		s.rt.sync(func() { s.rt.userMessage(d) })
	}
	brain := func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch phase.Load() {
		case past:
			r := say("Noted.")
			r.prompt = int(limit.Load()) + 5000
			return r
		case lookup:
			if m[len(m)-1]["role"] == "tool" {
				if refusals.Add(1) == 1 {
					// A message arrives meanwhile, which the restart
					// carries; the conversation it opens is past the
					// restart size on its first call.
					send(5)
					phase.Store(past)
				}
				return r7TooLong
			}
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
		}
		return hold()
	}
	s = startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 200_000, 2048
	})
	r5LogOnFailure(s)
	s.settled()
	s.rt.sync(func() { limit.Store(int64(s.rt.rolloverTokens())) })
	phase.Store(past)
	for i := 1; i <= 3; i++ {
		// The first restart, for size, and two for conversations past the
		// restart size on their first call.
		send(i)
		r6Until(t, fmt.Sprintf("restart %d", i), func() bool { return r6Dossiers(s) >= i })
	}
	s.settled()
	phase.Store(lookup)
	send(4)
	r6Until(t, "the user told the restarts stop", func() bool { return r6ToldFull(s) > 0 || r6Dossiers(s) > 4 })
	var notice string
	for _, m := range s.ui.said() {
		if strings.HasPrefix(m, fullNotice) {
			notice = m
		}
	}
	if !strings.Contains(notice, "past my restart size on its very first call") {
		t.Fatalf("restarts %d, and the user was told %q", r6Dossiers(s), notice)
	}
	promised := strings.Contains(notice, "when it reaches my model's window")
	// The conversation reaches the window.
	s.settled()
	phase.Store(lookup)
	send(6)
	const after = "even after I restarted it"
	r6Until(t, "a restart, or the user told the model refuses the conversation", func() bool {
		return r6Dossiers(s) > 4 || r5Told(s, "My model refuses my conversation as too long") > 0
	})
	if restarted := r6Dossiers(s) > 4; promised && !restarted {
		t.Fatalf("the user was promised a restart at the window, and none was made there (then told %q %d times): %q", after, r14Said(s, after), notice)
	}
}

// r14Said counts what the user was told that contains text.
func r14Said(s *session, text string) int {
	n := 0
	for _, m := range s.ui.said() {
		if strings.Contains(m, text) {
			n++
		}
	}
	return n
}

// The same while a restart cannot be made (the file for the next
// conversation could not be created, and the next try is minutes away):
// the refused second call of each turn is a failed call, and the turns
// after it wait longer each time.
func TestR14WhileARestartCannotBeMadeARefusedSecondCallWaitsLongerEachTime(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made read-only for root")
	}
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var ready, writing atomic.Bool
	brain := func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			writing.Store(true)
			p := places(ask)
			return reply{block: gate, text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case !ready.Load():
			return hold()
		case m[len(m)-1]["role"] == "tool":
			return r7TooLong
		}
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
	}
	s := startSession(t, "", brain, nil)
	t.Cleanup(release)
	r5LogOnFailure(s)
	for _, text := range []string{"The build is fixed.", "The linter is clean.", "The docs are updated."} {
		// Places for the briefing to cite, so that it is not sent back.
		r14AgentTurn(s, text)
		s.settled()
	}
	ready.Store(true)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	s.waitFor("the dossier call to go out", writing.Load)
	dir := s.rt.sess.Path
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	release()
	r6Until(t, "a wait of an hour, or calls going on without one", func() bool {
		return r4Logged(s, "trying again in") > 0 || r4Logged(s, "refused the prompt as too long")+r4Logged(s, "model call failed") > 30
	})
	if n := r4Logged(s, "the conversation continues as it is for now"); n != 1 {
		t.Fatalf("the restart was given up %d times, want once", n)
	}
	if n := r4Logged(s, "trying again in"); n != 1 {
		t.Fatalf("while no restart could be made, the refused calls went on without the waits between them growing (%d refusals, %d failed calls logged)", r4Logged(s, "refused the prompt as too long"), r4Logged(s, "model call failed"))
	}
	if n := r4Logged(s, "restarting the conversation from a dossier"); n != 1 {
		t.Fatalf("said %d times that the conversation is restarted from a dossier, and one restart was tried", n)
	}
}
