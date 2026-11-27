package harness

import (
	"fmt"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// After three restarts in a row each past the restart size on its first
// call, the user is told the restarts stop. The conversation then grows to
// the window and the provider refuses it. Calls of this conversation went
// through, so its opening fits the window, and a restart shrinks it back
// to an opening: it is restarted, and the user is answered. They are not
// told again that the restarts stopped.
func TestAConversationThatGrowsToTheWindowAfterTheBoundIsRestarted(t *testing.T) {
	r4ParallelSessions(t)
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	// Pictures arrive during each of the first three passes, so that run
	// of restarts ends in the bound; after it, nothing more arrives
	// during a pass.
	s, _ := r6QuietRestart(t, 200_000, func(s *session, pass int) {
		if pass > 3 {
			return
		}
		r6SendEach(s, 1, func(int) event.UserMessageData {
			return r6WithPictures(fmt.Sprintf("@e more screenshots, set %d", pass), 7, s)
		})
	})
	r6Until(t, "the user told the restarts stop", func() bool { return r6ToldFull(s) > 0 || r6Dossiers(s) > 5 })
	if n := r6Dossiers(s); n != 3 || r6ToldFull(s) != 1 {
		t.Fatalf("setup: %d restarts and told %d times, want 3 and 1", n, r6ToldFull(s))
	}
	r4Settled(s)
	noted := r5Told(s, "Noted.")
	// Six screenshots (96k tokens in the provider's count) on top of a
	// conversation past the 100k restart size: past the 183.6k a request
	// may hold.
	r6SendEach(s, 1, func(int) event.UserMessageData {
		return r6WithPictures("@e and these, from the phone", 6, s)
	})
	r6Until(t, "an answer after the window was reached", func() bool { return r5Told(s, "Noted.") > noted })
	t.Logf("%d restarts in all, told full %d times", r6Dossiers(s), r6ToldFull(s))
	if n := r6Dossiers(s); n < 4 {
		t.Fatalf("%d restarts, want the one more the window needed", n)
	}
	if n := r6ToldFull(s); n != 1 {
		t.Fatalf("the user was told %d times that the restarts stopped, want the one time before", n)
	}
	for _, m := range s.ui.said() {
		if m != rereadNotice && len(m) > 30 && m[:30] == "My model refuses my conversati" {
			t.Fatalf("the user was told the model refuses the conversation: %q", m)
		}
	}
}
