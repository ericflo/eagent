package harness

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
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

// ---- 6. the last call of a closing turn runs out of time ------------------

// With one call a turn, the closing turn starts an agent and tells the user
// about it, and the start outlasts the closing word's own time, so the
// message is not sent (interrupted). The session is not ending any more:
// the turn ends owing that call, and an ordinary turn makes it.
func TestR13LastClosingCallThatStartsAnAgentAndRunsOutOfTimeIsOwed(t *testing.T) {
	setFor(t, &finalReportTimeout, 200*time.Millisecond)
	first, second := newFakeSeat("First coat on."), newFakeSeat("Second coat on.")
	useSeatList(t, first, second)
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	inner := launchRelay // restored by useSeatList's cleanup
	var starts atomic.Int32
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		if starts.Add(1) == 2 {
			<-gate
		}
		return inner(spec, innerID, resume)
	}
	var phase atomic.Int32
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case phase.CompareAndSwap(2, 3):
			// The first call after the closing turn.
			return reply{calls: []event.ToolCall{tc("relay_send", `{"text":"paint the second coat"}`)}}
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			time.AfterFunc(2*finalReportTimeout, release)
			return reply{calls: []event.ToolCall{tc("relay_start", `{"profile":"codex"}`), tc("send_message", `{"text":"Starting Codex on the second coat."}`)}}
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	cfg := testConfig(model.srv.URL)
	cfg.MaxCallsPerTurn = 1
	s := audit2Start(t, cfg, Options{Prompt: brief}, nil, model, nil)
	sent := func() bool { return len(second.sentTexts()) > 0 }
	s.waitFor("the new agent's work, or the closing turn ended owing nothing", func() bool {
		return sent() || len(r11Ends(s, "timeout")) > 0
	})
	if !sent() {
		t.Fatalf("the closing turn ended %q after the session stopped ending, and the call it was cut off in is never made", r11Ends(s, "timeout"))
	}
}

// ---- 4. the closing word is asked again -----------------------------------

// The user restarts the stopped agent (/restart) while the first call of the
// closing turn is out, that call fails while the start is under way, and
// then the start fails. The session ends after all, and the user still gets
// a closing report: the closing word the failed call did not give is asked
// for again.
func TestR13ClosingWordIsAskedAgainAfterAFailedRestart(t *testing.T) {
	useFakeRelays(t, newFakeSeat("Merged #42; the tests are green."))
	inner := launchRelay // restored by useFakeRelays' cleanup
	entered := make(chan struct{}, 1)
	launchGate, callGate := make(chan struct{}), make(chan struct{})
	releaseLaunch, releaseCall := sync.OnceFunc(func() { close(launchGate) }), sync.OnceFunc(func() { close(callGate) })
	t.Cleanup(func() { releaseLaunch(); releaseCall() })
	var starts atomic.Int32
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		if starts.Add(1) == 2 {
			entered <- struct{}{}
			<-launchGate
			return nil, relay.Started{}, errors.New("tmux failed to start")
		}
		return inner(spec, innerID, resume)
	}
	var phase atomic.Int32
	s, _ := r12Start(t, nil, func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(2, 3):
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Final report: #42 is merged; the restart failed.","important":true}`)}}
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			r := rt.Load()
			r.sync(func() { r.onInputFrom("/restart", "web", "", "", nil) })
			return reply{block: callGate, status: r10Bad.status, body: r10Bad.body}
		case strings.Contains(tail, "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the restart never began")
	}
	releaseCall()
	s.waitFor("the failed closing call", func() bool { return len(s.events(event.Error)) > 0 })
	releaseLaunch()
	code := coreExit(s)
	if r5Told(s, "Final report") != 1 || code != 0 {
		t.Fatalf("exit %d, said %q; want the closing report after the failed restart", code, s.ui.said())
	}
}

// The same for the last title and summary: the user restarts the stopped
// agent while its first call is out, that call holds, and the start fails.
// The session ends after all, with the last title and summary written.
func TestR13LastSummaryIsAskedAgainAfterAFailedRestart(t *testing.T) {
	coreSummariesOn(t, false)
	useFakeRelays(t, newFakeSeat("Both coats are on."))
	inner := launchRelay // restored by useFakeRelays' cleanup
	entered := make(chan struct{}, 1)
	launchGate := make(chan struct{})
	releaseLaunch := sync.OnceFunc(func() { close(launchGate) })
	t.Cleanup(releaseLaunch)
	var starts atomic.Int32
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		if starts.Add(1) == 2 {
			entered <- struct{}{}
			<-launchGate
			return nil, relay.Started{}, errors.New("tmux failed to start")
		}
		return inner(spec, innerID, resume)
	}
	var phase atomic.Int32
	s, _ := r12Start(t, nil, func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		summary := strings.Contains(tail, "this look is only about the thread's title and summary")
		switch {
		case summary && phase.CompareAndSwap(2, 3):
			return reply{calls: []event.ToolCall{tc("update_thread", `{"title":"Paint both coats","summary":"Both coats are on; the restart failed."}`)}}
		case summary && phase.CompareAndSwap(1, 2):
			r := rt.Load()
			r.sync(func() { r.onInputFrom("/restart", "web", "", "", nil) })
			return hold()
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsAndReports("Done: both coats are on.")
		}
		return hold()
	})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the restart never began")
	}
	s.waitFor("the last summary's call", func() bool { return phase.Load() == 2 })
	s.settled()
	releaseLaunch()
	code := coreExit(s)
	if titles := coreTitles(mustReadCore(t, s.rt.sess.Path)); len(titles) == 0 || code != 0 {
		t.Fatalf("exit %d, titles %v, said %q; want the last title and summary written after the failed restart", code, titles, s.ui.said())
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
