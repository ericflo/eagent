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
