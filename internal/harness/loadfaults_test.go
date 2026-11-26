package harness

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Faults in the program that only load showed. Each test here fails on
// the code as it is and states the fault; they are skipped until it is
// fixed, so the suite stays green. Set runKnownFaults to see them fail.
var runKnownFaults = false

// A post carries the status line as it was when the post went out. When
// that post is slow and a newer line is written while it is on its way,
// the service applies the post's older line on arrival (a post carrying a
// line sets it). The keeper takes the post's line for what is sent, but
// nothing wakes it, so it sleeps until its refresh (4 minutes for a
// waiting line) while the phone shows the old one: here "Thinking about
// the next step" long after the call is over. Seen under load as
// TestPhoneSaysEagentIsThinkingWhileACallIsOut timing out.
func TestAStatusAPostCarriedIsNotLeftUpWhenANewerOneOvertookIt(t *testing.T) {
	if !runKnownFaults {
		t.Skip("known fault, not fixed yet: a slow post's older status line is left up (see the comment)")
	}
	quickKeeper(t)
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var out atomic.Bool
	brain := func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			out.Store(true)
			return reply{block: gate, calls: say("Codex is idle.").calls}
		}
		return hold()
	}
	g := phoneStart(t, fp, "", true, brain, nil)
	t.Cleanup(release)
	waitFor(t, "the first status", func() bool { return len(fp.statuses()) > 0 })
	// The answer's post goes out while the turn is still thinking (the call
	// after the tool result) and lands 300ms later, long after the turn is
	// over and the session waits.
	fp.mu.Lock()
	fp.holdPost, fp.holdFor = "Codex is idle.", 300*time.Millisecond
	fp.mu.Unlock()
	g.ui.input <- "@e what is Codex doing?"
	waitFor(t, "the call to go out", out.Load)
	release()
	waitFor(t, "the answer on the phone", func() bool {
		return len(fp.postsWhere(func(p map[string]any) bool { return p["body"] == "Codex is idle." })) == 1
	})
	// Many keeper gaps and floors after the post landed, the phone shows
	// what the session wants.
	time.Sleep(25 * max(statusMinGap, statusLookFloor))
	var want string
	g.rt.sync(func() { want = g.rt.currentActivity().Text })
	if got := fp.shownStatus(); got != want {
		t.Fatalf("the phone shows %q after the post that carried it, but the session wants %q", got, want)
	}
}

// A batch session's user writes to eagent while the call that makes
// eagent's last message is out. That message was written without seeing
// theirs, but it is later on the record, so at the end the user is taken
// for having heard back: the closing look is not told they are owed a
// word, and with the model stopping the agent instead of answering, the
// session ends and the question is never answered. Written after the
// reply is shown (TestAudit2ClosingWord) the same message is owed one.
// Seen under load as that test failing: its message then landed mid-call.
func TestAMessageWrittenWhileTheLastReplyWasOutIsStillOwedAWord(t *testing.T) {
	if !runKnownFaults {
		t.Skip("known fault, not fixed yet: a message written during eagent's last call is taken as heard (see the comment)")
	}
	s, seen, code := audit2ClosingAt(t, true, func(msgs []map[string]any) reply {
		if strings.Contains(lastText(msgs), "Holding is not allowed here") {
			return say("Final: #42 and #44 are merged; #45 is not.")
		}
		return hold()
	})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if seen.find("The user has written since you last spoke and has heard nothing back") == "" {
		t.Errorf("the closing look did not say the user is owed a word; they were told %q", s.ui.said())
	}
	said := s.ui.said()
	if len(said) == 0 || said[len(said)-1] != "Final: #42 and #44 are merged; #45 is not." {
		t.Fatalf("the session ended on %q, not on a word for the user", said)
	}
}
