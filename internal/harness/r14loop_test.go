package harness

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// Findings 4 and 5 of the fourteenth review: eagent's own closing words, and
// a closing turn whose session stopped ending part-way.

// ---- 4. what eagent's own closing words say runs --------------------------

// A batch session with no phone: the agent's pane goes away (it exited) while
// the call after its turn is out, that call asks the user, and the model
// cannot be reached after that. eagent's own closing words do not say the
// exited agent keeps running.
func TestR14ClosingWordsOverAnExitedAgentDoNotSayItRuns(t *testing.T) {
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Merged #42; #45 conflicts."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		if phase.Load() >= 1 {
			return r10Bad
		}
		if strings.Contains(lastUserText(msgs), "finished a turn") && phase.CompareAndSwap(0, 1) {
			r := rt.Load()
			r.sync(func() {
				r.onRelayEvent(r.st.ActiveRelays()[0].Name, relay.Event{Kind: relay.KindState, State: "exited", Detail: "the pane is gone"})
			})
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge #45 once the tests pass?","options":["yes","no"]}`)}}
		}
		return hold()
	})
	code := coreExit(s)
	if code != 2 || r5Told(s, "This session is ending") != 1 {
		t.Fatalf("exit %d, said %q; want exit 2 and eagent's own closing words", code, s.ui.said())
	}
	for _, m := range s.ui.said() {
		if strings.HasPrefix(m, "This session is ending") && (strings.Contains(m, "keeps running") || !strings.Contains(m, "Nothing is running now.")) {
			t.Fatalf("said %q with the agent exited", m)
		}
	}
}

// The same session with two agents running when it asks: eagent's own
// closing words name both, not only the first.
func TestR14ClosingWordsNameEveryAgentStillRunning(t *testing.T) {
	useSeatList(t, newFakeSeat("Merged #42; #45 conflicts."), newFakeSeat("Reviewing #45."))
	var phase atomic.Int32
	s, _ := r12Start(t, nil, func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		if phase.Load() >= 1 {
			return r10Bad
		}
		if strings.Contains(lastUserText(msgs), "finished a turn") && phase.CompareAndSwap(0, 1) {
			r13Turn(rt, "Rebased #45 onto main; the tests are running.")
			return reply{calls: []event.ToolCall{
				tc("relay_start", `{"profile":"claude"}`),
				tc("ask_user", `{"text":"Merge #45 once the tests pass?","options":["yes","no"]}`),
			}}
		}
		return hold()
	})
	code := coreExit(s)
	if code != 2 || r5Told(s, "This session is ending") != 1 {
		t.Fatalf("exit %d, said %q; want exit 2 and eagent's own closing words", code, s.ui.said())
	}
	var names []string
	for _, rel := range s.rt.st.ActiveRelays() {
		names = append(names, rel.Who())
	}
	want := strings.Join(names, " and ") + " keep running in their own terminals."
	for _, m := range s.ui.said() {
		if strings.HasPrefix(m, "This session is ending") && (len(names) != 2 || !strings.Contains(m, want)) {
			t.Fatalf("said %q with %q running; want %q", m, names, want)
		}
	}
}

// ---- 5. a closing turn whose session stopped ending ------------------------

// r14UI is the test UI with a hook on every event the session records, run
// on the loop goroutine as the event is written.
type r14UI struct {
	*fakeUI
	trace func(event.Event)
}

func (u *r14UI) Trace(ev event.Event) { u.trace(ev) }

// r14Start is r12Start whose trace runs on the loop for each event the
// session records, with the runtime.
func r14Start(t *testing.T, seat *fakeSeat, brain func(*atomic.Pointer[Runtime], []map[string]any) reply, trace func(*Runtime, event.Event)) *session {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, seat)
	var rt atomic.Pointer[Runtime]
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply { return brain(&rt, msgs) })
	ui := &r14UI{fakeUI: &fakeUI{input: make(chan string, 16)}, trace: func(ev event.Event) {
		if r := rt.Load(); r != nil {
			trace(r, ev)
		}
	}}
	r, err := New(testConfig(model.srv.URL), Options{Prompt: brief, Project: t.TempDir()}, ui)
	if err != nil {
		t.Fatal(err)
	}
	rt.Store(r)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s := &session{t: t, rt: r, ui: ui.fakeUI, seat: seat, model: model, cancel: cancel, done: make(chan int, 1)}
	go func() { s.done <- r.Run(ctx) }()
	t.Cleanup(s.stop)
	return s
}

// The closing turn's first call reads something, while a command's work
// keeps the session open (a slash command, say): the session stopped
// ending, and the turn ends "not closing". The command is done before the
// turn's end is settled. eagent does not tell the user it could not write a
// closing report before the turn that follows writes the real one.
func TestR14ClosingTurnThatStoppedClosingGetsNoFallbackBeforeTheReport(t *testing.T) {
	var phase atomic.Int32
	s := r14Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			r := rt.Load()
			r.sync(func() { r.commands++ })
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"#42"}`)}}
		case phase.CompareAndSwap(2, 3):
			// The call after the read, in the turn that follows.
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Final report: #42 is merged and verified.","important":true}`)}}
		case strings.Contains(tail, "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	}, func(r *Runtime, ev event.Event) {
		var d event.TurnData
		if ev.Type == event.TurnEnd && ev.Decode(&d) == nil && d.Reason == "not closing" && r.commands > 0 {
			r.commands-- // the command's work is done
		}
	})
	code := coreExit(s)
	if code != 0 || r5Told(s, "This session is ending") != 0 || r5Told(s, "Final report") != 1 {
		t.Fatalf("exit %d, said %q; want exit 0 and the closing report alone", code, s.ui.said())
	}
}

// The closing turn's first call fails while a command's work keeps the
// session open, and the command is done before the turn's end is settled.
// The session does not end in silence: the closing word the failed call did
// not give is asked for again.
func TestR14FailedClosingCallThatStoppedClosingIsAskedAgain(t *testing.T) {
	var phase atomic.Int32
	s := r14Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(2, 3):
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Final report: #42 is merged and verified.","important":true}`)}}
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			r := rt.Load()
			r.sync(func() { r.commands++ })
			return r10Bad
		case strings.Contains(tail, "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	}, func(r *Runtime, ev event.Event) {
		if ev.Type == event.Error && r.commands > 0 {
			r.commands-- // the command's work is done
		}
	})
	code := coreExit(s)
	if code != 0 || r5Told(s, "Final report") != 1 {
		t.Fatalf("exit %d, said %q; want exit 0 and the closing report", code, s.ui.said())
	}
}
