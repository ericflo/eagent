package harness

// Behaviours the old phone_test.go held that the one-loop code does not
// have. These fail today; they are kept out of the normal suite by the build
// tag. Run them with:
//
//	go test -tags gaps -run 'TestGapPhone' ./internal/harness/

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// Old TestRepeatReadsAreShortCircuited, the view_image half (read_file went
// with eagent's file tools): a second look at an unchanged picture comes
// back as a note that it has not changed, with no image, so the same
// picture is not sent to the model again. main did this in exec.go
// (execTool "view_image" -> unchangedSince / remember); the branch's
// runTool (toolcalls.go, tools.ViewImage) has no such check.
func TestGapPhoneRepeatViewImageIsShortCircuited(t *testing.T) {
	var mu sync.Mutex
	looks := 0
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case phoneAfterLook(msgs) && looks >= 2:
			return say("Seen it twice.")
		case phoneAfterLook(msgs), strings.Contains(lastUserText(msgs), "kept for you") && looks == 0:
			looks++
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"shot.png"}`)}}
		}
		return hold()
	}, nil)
	if err := os.WriteFile(filepath.Join(s.rt.opts.Project, "shot.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	s.type_("@e look at shot.png")
	s.waitFor("two looks", func() bool { return len(s.events(event.ToolResult)) >= 2 })
	s.settled()
	var views []event.ToolResultData
	for _, ev := range s.events(event.ToolResult) {
		var d event.ToolResultData
		_ = ev.Decode(&d)
		if d.Name == "view_image" {
			views = append(views, d)
		}
	}
	if len(views) != 2 || len(views[0].Images) != 1 || len(views[1].Images) != 0 || !strings.Contains(views[1].Output, "unchanged since you looked at it") {
		t.Fatalf("views = %+v", views)
	}
}

// Old TestPhoneDismissedQuestionIsNotAskedAgain, its ending: a batch session
// whose question the user dismissed, and who then wrote nothing in the
// question's time, ends awaiting input (exit 2), and the phone's closing
// note says it is waiting for them. main: runtime.go maybeEnd ->
// endReasonForIdle ("awaiting-input" when the orchestrator's last yield was
// not done or a question is open). The branch's maybeEnd ends it "done",
// exit 0, and the phone is told "eagent finished this session".
func TestGapPhoneDismissedQuestionEndsAwaitingInput(t *testing.T) {
	// The question's four seconds, at 100ms each: the session listens for
	// 400ms after the question, many times what the dismissal takes to arrive.
	setFor(t, &questionSecond, 100*time.Millisecond)
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "paint it", false, phoneDismissedBrain(), func(cfg *config.Config) { cfg.Finalechat.QuestionTimeoutSeconds = 4 })
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	if !fp.dismiss() {
		t.Fatal("nothing to dismiss")
	}
	code := g.exit(30 * time.Second)
	st := state.Replay(g.log())
	if code != 2 || st.EndReason != "awaiting-input" {
		t.Fatalf("exit %d, end reason %q; want 2 and awaiting-input", code, st.EndReason)
	}
	if got := fp.postsWhere(func(p map[string]any) bool { return p["sender"] == "system" }); len(got) != 1 || !strings.Contains(got[0]["body"].(string), "waiting for you") {
		t.Fatalf("closing note = %v", got)
	}
}

// Old TestPhoneReasksPendingQuestionOnResume: a session interrupted while a
// question is open asks it on the phone again when resumed, and the phone
// answer then finishes the work. main kept the resumed batch session alive
// until the mirror had re-asked (its maybeEnd waited on the orchestrator and
// the narrator's final word first, by which time startPhone's queued
// re-ask had marked the question as asked, phone.go waitingOnPhone). The
// branch's maybeEnd (runtime.go) runs on the first tick: no agent is
// active, waitingOnPhone is still false because the re-ask waits behind the
// mirror's first Me()/Post round trip, so the session ends at once,
// "awaiting-input", exit 2, and the question is never put back.
func TestGapPhoneReasksPendingQuestionOnResume(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "paint it", false, func(_ string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			if strings.Contains(all, "Done.") {
				return hold()
			}
			return say("Done.")
		case strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(all, "Which colour?"):
			return phoneAskAndStop("Which colour?", "red", "blue")
		}
		return hold()
	}, nil)
	waitFor(t, "the first phone question", func() bool { return fp.pendingQuestions() == 1 })
	waitFor(t, "the card to be recorded", func() bool {
		for _, ev := range g.events(event.PhoneQuestion) {
			_ = ev
			return true
		}
		return false
	})
	g.interrupt() // with the question open
	g.resume(Options{})
	waitFor(t, "the question to be asked again", func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return len(fp.asks) == 2 })
	if !fp.answer("red", "") {
		t.Fatal("no pending question")
	}
	if code := g.exit(30 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	st := state.Replay(g.log())
	if st.EndReason != "done" || st.Question != nil {
		t.Fatalf("end=%s question=%v", st.EndReason, st.Question)
	}
}

// Old TestPhoneStatusLineFollowsWorkAndPostsAreIdempotent: the phone's
// status line named the command running at that moment (then eagent's own
// bash, secrets blanked). Now the step is the agent's: relay.go records a
// KindActivity step and kicks the keeper, but never recomputes the wanted
// status (phone.noteState runs only from observe, on recorded events), so
// the phone goes on showing the stale line until some other event is
// recorded. main's relay.go had the same code for relays; the old test's
// path through bash, which did update it, is gone.
func TestGapPhoneStatusFollowsTheAgentsStep(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "", false, phoneStopAndClose("Ran it."), nil)
	g.seat.mu.Lock()
	g.seat.hang = true
	g.seat.mu.Unlock()
	waitFor(t, "the thread", func() bool { return fp.postCount() >= 1 })
	if err := PostInbox(g.rt.sess.Path, InboxMessage{Type: "message", Text: "run it", From: "web"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the message to reach the agent", func() bool { return len(g.seat.sentTexts()) == 1 })
	// Past the status's minimum gap, with no look of the keeper's pending:
	// the step alone must bring the line up.
	g.statusIdle()
	g.seat.emit(agentDoes("running go test ./..."))
	waitFor(t, "the status to name the step", func() bool {
		for _, a := range fp.statuses() {
			if a["text"] == "Codex: running go test ./..." {
				return true
			}
		}
		return false
	})
}
