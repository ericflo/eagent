package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// threadOnly is how the tail of a call made only for the title and summary
// reads.
const threadOnly = "this look is only about the thread's title and summary"

// A batch session whose model reports, stops the agent and sets the title
// and summary in one reply has given the thread list how it ended: the
// message it sent in that reply is what the summary was written with. No
// call is made afterwards only for the title and summary.
func TestAThreadUpdateInTheReplyThatReportsCoversThatReport(t *testing.T) {
	coreSummariesOn(t, false)
	var threadCalls atomic.Int32
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, threadOnly):
			threadCalls.Add(1)
			return reply{calls: []event.ToolCall{tc("update_thread", `{"summary":"Merged #42 and #44; Codex stopped."}`)}}
		case strings.Contains(tail, "finished a turn"):
			r := stopsAndReports("Codex merged #42 and #44; stopping it.")
			r.calls = append(r.calls, tc("update_thread", `{"title":"Merge pending PRs","summary":"Merged #42 and #44 on main; Codex stopped."}`))
			return r
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief + " Stop when done."}, newFakeSeat("Merged #42 and #44."), model, nil)
	if code := coreExit(s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := threadCalls.Load(); n != 0 {
		t.Fatalf("%d call(s) were made only for the title and summary after the reply that set them with its report", n)
	}
	evs := mustReadCore(t, s.rt.sess.Path)
	st := state.Replay(evs)
	if st.Description != "Merged #42 and #44 on main; Codex stopped." {
		t.Fatalf("the thread was not updated: %+v", coreTitles(evs))
	}
	// The record says the summary covers the end, so a resumed session does
	// not ask again either.
	var stopped int64
	for _, ev := range evs {
		if ev.Type == event.RelayEnd {
			stopped = ev.Seq
		}
	}
	if st.SummarySeq < stopped {
		t.Fatalf("the record's summary covers seq %d, the agent stopped at %d", st.SummarySeq, stopped)
	}
}

// The same at a terminal: a reply that answers the user and sets the title
// and summary leaves nothing for a later call about them, while news that
// is not the reply's own (the agent's next report) still gets one.
func TestAThreadUpdateInTheReplyThatAnswersCoversTheAnswer(t *testing.T) {
	coreSummariesOn(t, true)
	var threadCalls atomic.Int32
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, threadOnly):
			threadCalls.Add(1)
			return reply{calls: []event.ToolCall{tc("update_thread", `{"summary":"Codex is merging the PRs."}`)}}
		case strings.Contains(tail, "kept for you"):
			r := say("Codex is idle; nothing is running.")
			r.calls = append(r.calls, tc("update_thread", `{"title":"Codex status","summary":"Asked what Codex is doing; it is idle."}`))
			return r
		}
		return hold()
	}, nil)
	s.type_("@e what is Codex up to?")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	// A thread-only call would come one talk gap after the answer.
	time.Sleep(summaryWorkDelay + 3*summaryTalkGap)
	s.settled()
	if n := threadCalls.Load(); n != 0 {
		t.Fatalf("%d call(s) were made only for the title and summary after the reply that set them with its answer", n)
	}
	// News the reply did not make is still brought up.
	s.type_("merge the pending PRs")
	s.waitFor("a later look at the title and summary", func() bool { return threadCalls.Load() > 0 })
}
