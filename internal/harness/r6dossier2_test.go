package harness

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
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
	s, _ := r6QuietRestart(t, 200_000, func(s *session, pass int) {
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

// A known limit, pinned here as it is: the count of restarts in a row
// (restartRescues), like what the last restart carried, whether it was
// the one more try and whether no restart can make room was decided, is
// kept in memory. A session stopped while the first call after a restart
// is out starts counting again when it is resumed, so each such resume
// can buy up to two more restarts; within one run the bound holds and the
// user is told once. To be closed for all four together by putting them
// in the dossier's record.
func TestRestartsInARowStartCountingAgainAfterAResume(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	const window, ceiling, stops = 200_000, 16384, 2
	inner := r6Brain(window, ceiling, 100)
	var cur atomic.Pointer[session]
	var mu sync.Mutex
	armed := false // a pass has just landed: the next look is its conversation's first
	passes := 0    // dossier passes seen
	stopAt := 0    // the pass whose conversation's first call is held (0: none)
	var holdCh chan struct{}
	out := make(chan struct{}, 8)
	var held []chan struct{}
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			select {
			case <-c:
			default:
				close(c)
			}
		}
	})
	brain := func(model string, m []map[string]any) reply {
		if dossierRequest(m) != "" {
			mu.Lock()
			passes++
			n := passes
			mu.Unlock()
			// Pictures arrive during every pass: the new conversation's
			// first call is past the restart size, inside the window.
			if s := cur.Load(); s != nil {
				r6SendEach(s, 1, func(int) event.UserMessageData {
					return r6WithPictures(fmt.Sprintf("@e more screenshots, set %d", n), 7, s)
				})
			}
			r := inner(model, m)
			mu.Lock()
			armed = true
			mu.Unlock()
			return r
		}
		mu.Lock()
		hold := armed && stopAt > 0 && passes == stopAt
		armed = false
		var ch chan struct{}
		if hold {
			stopAt = 0
			ch = make(chan struct{})
			held = append(held, ch)
			holdCh = ch
		}
		mu.Unlock()
		if hold {
			out <- struct{}{}
			<-ch
		}
		return inner(model, m)
	}
	tune := func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = window, ceiling
	}
	s := startSession(t, "", brain, tune)
	cur.Store(s)
	r5LogOnFailure(s)
	r4Settled(s)
	limit := 0
	s.rt.sync(func() { limit = s.rt.rolloverTokens() })
	// Hold the first call of the conversation the second restart opens.
	mu.Lock()
	stopAt = 2
	mu.Unlock()
	r6SendEach(s, 1, func(int) event.UserMessageData {
		return r6WithPictures("@e hello, here is how it looks", limit/r6PictureTokens+1, s)
	})
	for i := 0; i < stops; i++ {
		select {
		case <-out:
		case <-time.After(10 * time.Second):
			t.Fatalf("run %d: the first call after a restart never went out (%d restarts, told %d)", i+1, len(s.events(event.Dossier)), r6ToldFull(s))
		}
		if r6ToldFull(s) > 0 {
			t.Fatalf("run %d: told before the stop", i+1)
		}
		cfg, path, project := s.rt.cfg, s.rt.sess.Path, s.rt.opts.Project
		cur.Store(nil)
		s.stop()
		mu.Lock()
		if i+1 < stops {
			stopAt = passes + 1
		}
		close(holdCh)
		mu.Unlock()
		s = audit2Resume(t, cfg, Options{Project: project, Interactive: true}, path, s.seat, s.model)
		cur.Store(s)
		r5LogOnFailure(s)
	}
	r6Until(t, "the user told the restarts stop", func() bool { return r6ToldFull(s) > 0 || len(s.events(event.Dossier)) > 8 })
	n := len(s.events(event.Dossier))
	// Without stops this is 3. Each of the two stops lands while the first
	// call after a restart is out and starts the count again, which buys
	// two more: 5 in all, and then the bound holds and the user is told.
	if n != 5 {
		t.Fatalf("%d restarts in a row before the user was told, want 5 (3, and the count started again after each of %d stops)", n, stops)
	}
	if told := r6ToldFull(s); told != 1 {
		t.Fatalf("the user was told %d times, want once", told)
	}
}
