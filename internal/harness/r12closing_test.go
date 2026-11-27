package harness

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// Findings 1-5 of the twelfth review: a closing turn (the closing word, or
// the last title and summary) that stops being one part-way.

// r12Write records a message from the user on rt's loop, as the terminal or
// the phone would, from inside a scripted reply (the call is still out).
func r12Write(rt *atomic.Pointer[Runtime], text string) {
	r := rt.Load()
	r.sync(func() { r.userMessage(event.UserMessageData{Text: text}) })
}

// r12Start is audit2Start for a batch session whose runtime the model's
// replies can reach (r12Write).
func r12Start(t *testing.T, seat *fakeSeat, brain func(*atomic.Pointer[Runtime], []map[string]any) reply) (*session, *scripted) {
	t.Helper()
	var rt atomic.Pointer[Runtime]
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply { return brain(&rt, msgs) })
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, seat, model, func(r *Runtime) { rt.Store(r) })
	return s, model
}

// r12Ends is r11Ends read from the record on disk, for a session that has
// ended.
func r12Ends(t *testing.T, s *session, reason string) []string {
	var out []string
	for _, ev := range mustReadCore(t, s.rt.sess.Path) {
		var d event.TurnData
		if ev.Type == event.TurnEnd && ev.Actor == event.ActorEagent && ev.Decode(&d) == nil && d.Reason == reason {
			out = append(out, d.Reason)
		}
	}
	return out
}

// ---- 1. the closing turn's own time --------------------------------------

// The closing turn starts an agent, and the call after the start takes
// longer than the closing word's own time. The session is not ending any
// more: that call is not cut off by the closing word's time, and the new
// agent gets its work.
func TestR12ClosingTurnThatStartsAnAgentHasNoClosingTimeLimit(t *testing.T) {
	setFor(t, &finalReportTimeout, 200*time.Millisecond)
	slow := 2 * finalReportTimeout
	first, second := newFakeSeat("First coat on."), newFakeSeat("Second coat on.")
	useSeatList(t, first, second)
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	var phase atomic.Int32
	s, _ := r12Start(t, nil, func(_ *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		switch {
		case phase.CompareAndSwap(2, 3):
			// The call after the start.
			time.AfterFunc(slow, release)
			return reply{block: gate, calls: []event.ToolCall{tc("relay_send", `{"text":"paint the second coat"}`)}}
		case last == "tool":
			return hold()
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			return reply{calls: []event.ToolCall{tc("relay_start", `{"profile":"codex"}`)}}
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(3, 4):
			return stopsAndReports("Done: both coats are on.")
		}
		return hold()
	})
	sent := func() bool { return len(second.sentTexts()) > 0 }
	s.waitFor("the new agent's work, or the call cut off", func() bool {
		return sent() || len(r11Ends(s, "timeout")) > 0
	})
	if !sent() {
		t.Fatalf("the call after the start was cut off by the closing word's time (turn ends %q), and the new agent never got its work", r11Ends(s, "timeout"))
	}
	if code := coreExit(s); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

// The closing turn sets a timer, and the session is stopped (Ctrl-C) while
// the call after it is out. That call belongs to a session that is not
// ending: it is stopped with the session, which does not wait out the
// closing word's own time for it.
func TestR12ClosingTurnThatSetsATimerStopsWithTheSession(t *testing.T) {
	entered := make(chan struct{}, 1)
	gate := make(chan struct{}) // never opened: the call ends when its request is given up
	t.Cleanup(func() { close(gate) })
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Deployed to staging."), func(_ *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case phase.CompareAndSwap(2, 3):
			entered <- struct{}{}
			return reply{block: gate, calls: say("I'll check that staging is still up in an hour.").calls}
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			return reply{calls: []event.ToolCall{tc("schedule", `{"spec":"1h","note":"Check that staging is still up."}`)}}
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the call after the timer was never made")
	}
	stopped := time.Now()
	s.cancel()
	select {
	case code := <-s.done:
		s.done <- code
	case <-time.After(time.Second):
		t.Fatalf("the session did not stop within a second (%s so far): the call after the timer kept the closing word's own time and grace", time.Since(stopped).Round(time.Millisecond))
	}
}

// The last title and summary of a batch session reads something, and the
// user writes while that call is out. The session is not ending any more:
// the call for their message is not cut off by the last summary's time
// limit, and they get their answer from it.
func TestR12LastSummaryTheUserWritesDuringHasNoTimeLimit(t *testing.T) {
	coreSummariesOn(t, false)
	setFor(t, &summaryFinalWait, 200*time.Millisecond)
	slow := 2 * summaryFinalWait
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Both coats are on."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		summary := strings.Contains(tail, "this look is only about the thread's title and summary")
		switch {
		case strings.Contains(tail, "The session is ending now"):
			return hold() // the closing word: the report covers it
		case phase.CompareAndSwap(2, 3):
			// The first call after the read: the user's message is in it.
			time.AfterFunc(slow, release)
			return reply{block: gate, calls: say("Yes, coat 2 is dry.").calls}
		case last != "tool" && !summary && phase.CompareAndSwap(3, 4):
			return say("Yes, coat 2 is dry.") // made again for their message
		case last == "tool":
			return hold()
		case summary && phase.CompareAndSwap(1, 2):
			r12Write(rt, "did coat 2 dry?")
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"coats"}`)}}
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsAndReports("Done: both coats are on.")
		}
		return hold()
	})
	code := coreExit(s)
	if phase.Load() < 3 {
		t.Fatalf("the last summary never read anything with the user writing (phase %d)", phase.Load())
	}
	if ends := r12Ends(t, s, "timeout"); len(ends) > 0 || r5Told(s, "Yes, coat 2") != 1 || code != 0 {
		t.Fatalf("exit %d, turn ends %q, said %q; want the answer from the first call made for it, no call cut off", code, ends, s.ui.said())
	}
}

// ---- 2. eagent's own closing words ----------------------------------------

// The user writes while the first call of the closing turn is out, and that
// call fails. The session is not ending: eagent does not say it is, and the
// user gets one answer.
func TestR12MessageDuringAFailingClosingCallGetsNoClosingWords(t *testing.T) {
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			r12Write(rt, "is #42 merged?")
			return r10Bad
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

// The user writes while the first call of the closing turn is out, and that
// call holds, as does the one after it. The turn ends with nothing said;
// the session is not ending, so eagent does not close in its own words.
func TestR12MessageDuringAHoldingClosingCallGetsNoClosingWords(t *testing.T) {
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(lastText(msgs), "Holding is not allowed here"):
			return hold()
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

// The user restarts the stopped agent (/restart) while the first call of
// the closing turn is out, and that call fails once the agent is back. An
// agent is running: eagent does not say that nothing is.
func TestR12RestartDuringAFailingClosingCallGetsNoClosingWords(t *testing.T) {
	useRelayFactory(t, "Coat one is on.")
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	var phase atomic.Int32
	s, _ := r12Start(t, nil, func(_ *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			return reply{block: gate, status: r10Bad.status, body: r10Bad.body}
		case strings.Contains(tail, "finished a turn") && last != "tool" && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	s.waitFor("the closing turn's call", func() bool { return phase.Load() == 2 })
	s.rt.sync(func() { s.rt.onInputFrom("/restart", "web", "", "", nil) })
	s.waitFor("the agent back", func() bool {
		n := 0
		s.read(func(st *state.State) { n = len(st.ActiveRelays()) })
		return n == 1
	})
	release()
	s.waitFor("the failed call", func() bool { return len(s.events(event.Error)) > 0 })
	s.settled()
	if r5Told(s, "This session is ending") != 0 {
		t.Fatalf("said %q with the agent restarted", s.ui.said())
	}
}

// ---- 3. the closing reminder ----------------------------------------------

// The first call of the closing turn sets the title and says nothing, and
// the user writes while it is out. The call after it is the user's: it is
// not told that the session is ending and that they are owed a last word,
// and no call has that reminder taken back out of its prompt.
func TestR12ClosingTurnTheUserWritesDuringIsNotRemindedOfTheEnd(t *testing.T) {
	const ending = "The session is ending and the user is owed a last word"
	var phase atomic.Int32
	s, model := r12Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			r12Write(rt, "is #42 merged?")
			return reply{calls: []event.ToolCall{tc("update_thread", `{"title":"Merge #42","summary":"#42 is merged; the tests are green."}`)}}
		case phase.CompareAndSwap(2, 3):
			return say("Yes, #42 is merged and the tests are green.")
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	code := coreExit(s)
	for _, msgs := range audit2Requests(model) {
		if strings.Contains(allText(msgs), "is #42 merged?") && strings.Contains(lastText(msgs), ending) {
			t.Fatalf("a call made after the user wrote ends %q", lastText(msgs))
		}
	}
	if said := s.ui.said(); code != 0 || len(said) != 1 || !strings.HasPrefix(said[0], "Yes, #42") {
		t.Fatalf("exit %d, said %q; want exit 0 and the answer alone", code, said)
	}
}
