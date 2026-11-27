package harness

import (
	"strings"
	"sync/atomic"
	"testing"

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
