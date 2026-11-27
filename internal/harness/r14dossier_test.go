package harness

import (
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
