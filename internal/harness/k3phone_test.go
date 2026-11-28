package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A question that went to the thread as a message is answered by a resume
// (--answer); the thread is told once. A second resume, later, must not
// tell it again: nothing was settled by it.
func TestRV16ASettledNoteIsNotPostedAgainByALaterResume(t *testing.T) {
	for _, keys := range []bool{true, false} {
		name := "keys"
		if !keys {
			name = "no keys"
		}
		t.Run(name, func(t *testing.T) {
			fp := newPhoneFake(true)
			t.Cleanup(fp.srv.Close)
			if !keys {
				fp.features = []string{"activity", "dismiss", "attachments"}
			}
			g := r8AskSession(t, fp, "Which branch?", r8Choices(21))
			waitFor(t, "the question in the thread", func() bool { return k1AskedAsMessage(fp, "Which branch?") == 1 })
			g.agentStopped()
			r14Drained(t, g)
			g.rt.sync(func() {})
			g.interrupt()
			g.resume(Options{Interactive: true, Answer: "branch-03"})
			waitFor(t, "the thread to be attached", func() bool { return len(g.events(event.PhoneThread)) == 2 })
			r14Drained(t, g)
			if n := k1Settled(fp); n != 1 {
				t.Fatalf("after the first resume: %d notes that the question is settled, want 1", n)
			}
			g.rt.sync(func() {})
			g.interrupt()
			g.resume(Options{Interactive: true, Prompt: "go on"})
			waitFor(t, "the thread to be attached again", func() bool { return len(g.events(event.PhoneThread)) == 3 })
			r14Drained(t, g)
			if n := k1Settled(fp); n != 1 {
				t.Fatalf("after the second resume: %d notes that the question is settled, want 1", n)
			}
		})
	}
}
