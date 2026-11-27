package harness

import (
	"os"
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
