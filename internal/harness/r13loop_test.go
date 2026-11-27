package harness

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Findings 1-6 of the thirteenth review: what stillClosing is asked, and
// where it was not.

// ---- 3. what keeps a session open -----------------------------------------

// The user upgrades the stopped agent (/upgrade) while the first call of the
// closing turn is out, and that call fails. The upgrade's work keeps the
// session open, as maybeEnd has it: eagent does not say the session is
// ending with nothing running.
func TestR13UpgradeDuringAFailingClosingCallGetsNoClosingWords(t *testing.T) {
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	old := upgradeAgent
	upgradeAgent = func(string) (string, error) { <-gate; return "upgraded", nil }
	t.Cleanup(func() { release(); upgradeAgent = old })
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			r := rt.Load()
			r.sync(func() { r.onInputFrom("/upgrade", "web", "", "", nil) })
			return r10Bad
		case strings.Contains(tail, "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	s.waitFor("the failed closing call", func() bool { return len(s.events(event.Error)) > 0 })
	release()
	s.waitFor("the upgrade's outcome", func() bool { return saidWith(s.ui, "Upgraded") })
	s.settled()
	if r5Told(s, "This session is ending") != 0 {
		t.Fatalf("said %q, with the agent upgraded and running again", s.ui.said())
	}
}

// ---- 1. the first call of a turn ------------------------------------------

// The closing turn is started, and the user writes before its first call
// goes out. The session is not ending: no call is told it is, under the
// user's unanswered message, and they get their answer.
func TestR13MessageBeforeTheFirstClosingCallIsNotToldTheSessionEnds(t *testing.T) {
	const asked, answer = "wait, do not end: is #42 merged?", "Yes, #42 is merged."
	var phase atomic.Int32
	var told atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(allText(msgs), asked) && phase.CompareAndSwap(1, 2):
			// The first call made with the user's message in it.
			if strings.Contains(tail, "The session is ending now") {
				told.Add(1)
			}
			return say(answer)
		case strings.Contains(tail, "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" && phase.CompareAndSwap(0, 1):
			// Held open (a command's work, say) until the test starts
			// the closing turn itself.
			r := rt.Load()
			r.sync(func() { r.commands++ })
			return stopsWhenDone()
		}
		return hold()
	})
	s.waitFor("the agent stopped", func() bool { return phase.Load() == 1 && len(s.events(event.RelayEnd)) > 0 })
	s.settled()
	// The closing turn starts, and in the same moment the user writes:
	// both before its first call is built.
	s.rt.sync(func() {
		s.rt.commands--
		s.rt.maybeEnd()
		s.rt.userMessage(event.UserMessageData{Text: asked})
	})
	code := coreExit(s)
	if n := told.Load(); n > 0 || r5Told(s, answer) != 1 || code != 0 {
		t.Fatalf("%d call(s) with the user's message unanswered were told the session is ending; exit %d, said %q", n, code, s.ui.said())
	}
}

// The closing turn is started, and the user restarts the stopped agent
// (/restart) before its first call goes out. The session is not ending:
// no call is told that it is and that no agent is running.
func TestR13RestartBeforeTheFirstClosingCallIsNotToldTheSessionEnds(t *testing.T) {
	useRelayFactory(t, "Coat one is on.")
	var phase, told atomic.Int32
	s, _ := r12Start(t, nil, func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now"):
			told.Add(1)
		case strings.Contains(tail, "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" && phase.CompareAndSwap(0, 1):
			r := rt.Load()
			r.sync(func() { r.commands++ }) // held open until the test starts the closing turn
			return stopsWhenDone()
		}
		return hold()
	})
	s.waitFor("the agent stopped", func() bool { return phase.Load() == 1 && len(s.events(event.RelayEnd)) > 0 })
	s.settled()
	s.rt.sync(func() {
		s.rt.commands--
		s.rt.maybeEnd()
		s.rt.onInputFrom("/restart", "web", "", "", nil)
	})
	s.waitFor("the agent back", func() bool { return saidWith(s.ui, "Restarted") })
	s.settled()
	if n := told.Load(); n > 0 {
		t.Fatalf("%d call(s) were told the session is ending with the agent being restarted; said %q", n, s.ui.said())
	}
}

// ---- 2. a holding closing call --------------------------------------------

// The user writes while the first call of the closing turn is out, and that
// call holds. The session is not ending: the hold is not sent back with
// "the session is ending and the user is owed a last word", and the model
// obeying it does not send a closing report; the user gets one answer.
func TestR13MessageDuringAHoldingClosingCallIsNotSentBack(t *testing.T) {
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(lastText(msgs), "Holding is not allowed here"):
			return say("Session is ending; final report: #42 merged.")
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			r12Write(rt, "is #42 merged?")
			return hold()
		case phase.CompareAndSwap(2, 3):
			return say("Yes, #42 is merged and the tests are green.")
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	code := coreExit(s)
	if said := s.ui.said(); code != 0 || len(said) != 1 || !strings.HasPrefix(said[0], "Yes, #42") {
		t.Fatalf("exit %d, said %q; want exit 0 and the answer alone", code, said)
	}
}

// A look about the thread's title alone is started, and the agent ends a
// turn before its first call goes out. That call is told about the turn
// end, and the look the turn end asks for is not used up by a call told
// that nothing has happened.
func TestR13TurnEndBeforeTheFirstCallOfATitleLookIsLookedAt(t *testing.T) {
	const report = "CRITICAL BUILD FAILURE: tests broken on main."
	var looked atomic.Int32
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if strings.Contains(allText(msgs), report) && strings.Contains(lastUserText(msgs), "finished a turn") {
			looked.Add(1)
		}
		return hold()
	}, nil)
	s.waitFor("the agent's first turn", func() bool { return len(s.events(event.RelayTurn)) > 0 })
	s.settled()
	s.rt.sync(func() {
		s.rt.startTurn(wake{thread: true})
		name := s.rt.st.ActiveRelays()[0].Name
		s.rt.append(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: name, Text: report}))
	})
	s.settled()
	if looked.Load() == 0 {
		t.Fatal("the agent's turn end was never looked at: the title look's first call was told nothing had happened")
	}
}
