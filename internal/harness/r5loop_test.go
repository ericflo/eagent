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

// The user answers eagent's question while the turn that asked it is still
// going: the first call asked and looked at something, and a later call is
// the first to see the answer. A hold on it is sent back once, as it would
// be in a turn of its own: asking the question did nothing with its answer.
func TestAnAnswerArrivingBetweenTheCallsOfATurnIsNotTakenAsDealtWith(t *testing.T) {
	var mu sync.Mutex
	var phase, reminded int
	var sawAnswer bool
	gate := make(chan struct{})
	called := make(chan struct{}, 1)
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		tail := lastUserText(msgs)
		afterTool := msgs[len(msgs)-1]["role"] == "tool"
		switch {
		case strings.Contains(lastText(msgs), "The user's answer has reached nobody but you"):
			reminded++
			return hold()
		case strings.Contains(tail, "The user answered your question"):
			sawAnswer = true
			return hold()
		case afterTool && phase == 1:
			// The look's follow-up; the user answers while it is out.
			phase = 2
			select {
			case called <- struct{}{}:
			default:
			}
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"merge"}`)}, block: gate}
		case afterTool:
			return hold()
		case strings.Contains(tail, "kept for you") && phase == 0:
			phase = 1
			return reply{calls: []event.ToolCall{
				tc("ask_user", `{"text":"Merge #45 as well?","options":["yes","no"]}`),
				tc("session_search", `{"query":"#45"}`),
			}}
		}
		return hold()
	}, nil)
	s.type_("@e ask me whether to merge #45")
	<-called
	s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
	s.type_("yes")
	s.waitFor("the answer", func() bool { return len(s.events(event.UserAnswer)) == 1 })
	close(gate)
	s.settled()
	mu.Lock()
	defer mu.Unlock()
	if !sawAnswer {
		t.Fatal("no call was told the user answered")
	}
	if reminded != 1 {
		t.Fatalf("the held answer was sent back %d times, want 1", reminded)
	}
}

// The last title and summary of a batch session is one attempt. When that
// call fails, the session ends as it would have without it: no second try
// at once, no word to the user that the model cannot be reached after the
// closing report went out, no restart of the conversation, and the exit
// code of a session that finished its work.
func TestAFailedLastSummaryLeavesTheBatchSessionsEndAsItWas(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"refused", `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`},
		{"too long", tooLongBody},
	} {
		t.Run(c.name, func(t *testing.T) {
			coreSummariesOn(t, false)
			var threadCalls, after atomic.Int32
			model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
				tail := lastUserText(msgs)
				if threadCalls.Load() > 0 {
					after.Add(1)
				}
				switch {
				case msgs[len(msgs)-1]["role"] == "tool":
					return hold()
				case strings.Contains(tail, threadOnly):
					threadCalls.Add(1)
					return reply{status: 400, body: c.body}
				case strings.Contains(tail, "finished a turn"):
					return stopsAndReports("Codex merged #42 and #44; stopping it.")
				}
				return hold()
			})
			s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief + " Stop when done."}, newFakeSeat("Merged #42 and #44."), model, nil)
			code := coreExit(s)
			said := s.ui.said()
			if code != 0 {
				t.Fatalf("exit %d after a failed last summary; said %q", code, said)
			}
			if n := threadCalls.Load(); n != 1 {
				t.Fatalf("the last summary was asked for %d times, want once", n)
			}
			if n := after.Load(); n != 0 {
				t.Fatalf("%d more model call(s) after the failed last summary", n)
			}
			if last := said[len(said)-1]; !strings.HasPrefix(last, "Codex merged") {
				t.Fatalf("the user was told %q after the closing report", last)
			}
		})
	}
}
