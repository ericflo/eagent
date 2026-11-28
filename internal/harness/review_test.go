package harness

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// What an independent read of the audit fixes found, one test each.

// A turn that acted on a message kept for eagent and then holds has
// answered it: the hold is not sent back as "you never answered".
func TestHoldAfterActingIsNotSentBack(t *testing.T) {
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastText(msgs), "kept for you") {
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"the user asked"}`)}}
		}
		return hold()
	}, nil)
	s.waitFor("the agent's first turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	s.type_("@e stop codex")
	s.waitFor("the agent to be stopped", func() bool { return len(s.events(event.RelayEnd)) == 1 })
	s.settled()
	if n := audit2CountLast(s.model, "Holding is not an answer"); n != 0 {
		t.Fatalf("a hold after the stop was sent back %d time(s)", n)
	}
	if n := len(audit2Results(s.events(event.ToolResult), "relay_control")); n != 1 {
		t.Fatalf("relay_control ran %d times", n)
	}
}

// A closing look that starts an agent has not ended the session: its next
// hold stands, and the user is not told "nothing is running now".
func TestClosingLookThatStartsAnAgentOwesNoLastWord(t *testing.T) {
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		last := lastText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(last, "The session is ending now"):
			return reply{calls: []event.ToolCall{tc("relay_start", `{"profile":"codex"}`)}}
		case strings.Contains(last, "finished a turn"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"done"}`)}}
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, newFakeSeat("Merged #42."), model, nil)
	s.waitFor("the agent started by the closing look", func() bool { return len(s.events(event.RelayStart)) == 2 })
	select {
	case code := <-s.done:
		s.done <- code
		t.Fatalf("the session ended (%d) with the agent it had just started", code)
	case <-time.After(500 * time.Millisecond):
	}
	if n := audit2CountLast(s.model, "Holding is not allowed here"); n != 0 {
		t.Fatalf("the hold after the start was sent back %d time(s)", n)
	}
	for _, said := range s.ui.said() {
		if strings.Contains(said, "Nothing is running now") {
			t.Fatalf("the user was told %q with an agent running", said)
		}
	}
}

// A turn stopped by its own time limit is a timeout. Only a turn stopped
// with the session is cut off, and only that makes a resume say so.
func TestATimeLimitIsNotACutOffTurn(t *testing.T) {
	late, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if got := stopReason(late); got != "timeout" {
		t.Fatalf("a turn past its deadline ended %q", got)
	}
	stopped, stop := context.WithCancel(context.Background())
	stop()
	if got := stopReason(stopped); got != "interrupted" {
		t.Fatalf("a turn stopped with the session ended %q", got)
	}
	// Under a session that is stopping, a time limit still reads as cut off
	// only when it was the stop that came first.
	parent, stopParent := context.WithCancel(context.Background())
	child, done := context.WithTimeout(parent, time.Hour)
	defer done()
	stopParent()
	if got := stopReason(child); got != "interrupted" {
		t.Fatalf("a bounded turn stopped with the session ended %q", got)
	}

	// A session whose last turn ended on a timeout resumes with no notice.
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	s.rt.sync(func() {
		s.rt.append(event.New(event.TurnStart, event.ActorEagent, event.TurnData{Reason: "thread"}))
		s.rt.append(event.New(event.TurnEnd, event.ActorEagent, event.TurnData{Reason: "timeout"}))
	})
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()
	s2, _ := coreResume(t, path, project, s.model, newFakeSeat("ok"))
	s2.settled()
	for _, ev := range s2.events(event.HarnessMessage) {
		var d event.HarnessMessageData
		if ev.Decode(&d) == nil && strings.Contains(d.Text, "restarted in the middle of a turn") {
			t.Fatal("a turn that ran out of time was reported as cut off by a restart")
		}
	}
}

// The last summary's time limit belongs to a session that is ending. Once
// the user writes, the session is not ending, and the limit is gone.
func TestLastSummaryLimitEndsWhenTheUserWrites(t *testing.T) {
	coreSummariesOn(t, true)
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	s.rt.sync(func() { s.rt.summary.final = true })
	s.type_("carry on with the upgrade")
	s.waitFor("the message", func() bool { return len(s.events(event.UserMessage)) == 1 })
	var final bool
	s.rt.sync(func() { final = s.rt.summary.final })
	if final {
		t.Fatal("the session still counts as writing its last summary after the user wrote")
	}
}

// A session with no terminal that never had an agent ends awaiting-input
// only when a start was tried and failed. One that was resumed without an
// agent and answered the user has nothing waiting.
func TestAgentlessSessionEndsDoneUnlessAStartFailed(t *testing.T) {
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		last := lastText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(last, "never mind"):
			return say("All right, nothing more to do.")
		case strings.Contains(last, "could not be started"):
			return say("I could not start Codex here: the launcher failed.")
		}
		return hold()
	})
	first := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, nil, model, nil)
	if code := coreExit(first); code != 2 {
		t.Fatalf("a session whose agent could not start ended with exit %d", code)
	}
	path, project := first.rt.sess.Path, first.rt.opts.Project
	first.stop()

	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := Resume(testConfig(model.srv.URL), Options{Project: project, Prompt: "@e never mind, thanks"}, ui, path)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, rt: rt, ui: ui, model: model, done: make(chan int, 1)}
	ctx, cancel := contextWithCancel()
	s.cancel = cancel
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	if code := coreExit(s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if r := state.Replay(mustReadCore(t, path)).EndReason; r != "done" {
		t.Fatalf("end reason %q", r)
	}
}
