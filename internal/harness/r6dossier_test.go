package harness

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// r6Excerpt is a log excerpt of about 82 KiB that the user pastes to
// eagent, the i-th of a burst: one line of the record, ending in a mark
// that says which it is.
func r6Excerpt(i int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@e excerpt %d of the build log:\n", i)
	for b.Len() < 82*1024 {
		fmt.Fprintf(&b, "2026-11-25T10:%02d:%02d worker-%d step %d ok: compiled internal/harness in 1.9s\n", b.Len()%60, b.Len()%59, i, b.Len())
	}
	fmt.Fprintf(&b, "END-OF-EXCERPT-%d", i)
	return b.String()
}

// r6Burst is how many 82 KiB excerpts are more than a route of this window
// can take in one request.
func r6Burst(window int) int {
	return window/(82*1024*2/7) + 1
}

// r6QuietRestart runs a session on a route of the given window, whose
// provider counts as eagent estimates and refuses what is past the window.
// The first answer is past the restart size, so the conversation is
// restarted at the next quiet moment with nothing unseen: that restart
// carries nothing. While its pass is held open, during(s) runs; then the
// pass is let go. passes > 1 are held too when hold says so. It returns
// once the user's last message is answered, the user is told the
// conversation is full, or there are more restarts than any rule allows.
func r6QuietRestart(t *testing.T, window int, during func(s *session, pass int)) (*session, *r5PictureRoute) {
	setFor(t, &errorRetryDelays, []time.Duration{5 * time.Millisecond})
	route := &r5PictureRoute{window: window, ceiling: 16384, picture: 30000}
	inner := route.brain()
	var passes, answers atomic.Int32
	held := make(chan int, 8)
	release := make(chan struct{}, 8)
	brain := func(model string, m []map[string]any) reply {
		if dossierRequest(m) != "" {
			n := int(passes.Add(1))
			held <- n
			<-release
			return inner(model, m)
		}
		r := inner(model, m)
		if r.status == 0 && answers.Add(1) == 1 {
			r.prompt = window * 3 / 5 // past the restart size: the next quiet moment restarts
		}
		return r
	}
	s := startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	})
	t.Cleanup(func() {
		for i := 0; i < 8; i++ {
			release <- struct{}{}
		}
	})
	r5LogOnFailure(s)
	s.settled()
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "@e hello", Route: "eagent"}) })
	go func() {
		for n := range held {
			during(s, n)
			release <- struct{}{}
		}
	}()
	r4Until(t, "the answer, the conversation said to be full, or too many restarts", func() bool {
		return r5Told(s, "Noted.") >= 2 || r6ToldFull(s) > 0 || len(s.events(event.Dossier)) > 4
	})
	return s, route
}

// r6ToldFull counts what the user was told that says no restart can make
// room.
func r6ToldFull(s *session) int {
	n := 0
	for _, m := range s.ui.said() {
		if strings.HasPrefix(m, fullNotice) || strings.HasPrefix(m, "My conversation is full") {
			n++
		}
	}
	return n
}

// r6SendBurst sends n excerpts to eagent and returns their events.
func r6SendBurst(s *session, n int) []event.Event {
	var out []event.Event
	for i := 1; i <= n; i++ {
		text := r6Excerpt(i)
		s.rt.sync(func() {
			s.rt.userMessage(event.UserMessageData{Text: text, Route: "eagent"})
			out = append(out, s.rt.st.Events[len(s.rt.st.Events)-1])
		})
	}
	return out
}

// r6Reachable checks that the newest dossier tells the model plainly that
// these messages were not seen, with each one's place, and that each can be
// read whole from there with session_read.
func r6Reachable(t *testing.T, s *session, sent []event.Event) {
	t.Helper()
	ds := s.dossiers()
	brief := ds[len(ds)-1].Text
	at := strings.Index(brief, unseenHeading)
	if at < 0 {
		t.Fatalf("the newest dossier does not say the user sent things it has not seen:\n%.3000s", brief)
	}
	block := brief[at:]
	if end := strings.Index(block, "\n\n"); end > 0 {
		block = block[:end]
	}
	if !strings.Contains(block, fmt.Sprintf("%d messages", len(sent))) {
		t.Fatalf("the dossier's note does not say how many messages were not seen:\n%s", block)
	}
	read := func(args string) string {
		out, isErr, _ := s.rt.runTool(context.Background(), &turnCalls{}, tc("session_read", args))
		if isErr {
			t.Fatalf("%s: %s", args, out)
		}
		return out
	}
	for i, ev := range sent {
		place := fmt.Sprintf("%s:%d", ev.Source.File, ev.Source.Line)
		if !strings.Contains(block, place) {
			t.Fatalf("the dossier's note does not give the place of excerpt %d (%s):\n%s", i+1, place, block)
		}
		got := r2Follow(t, read, ev.Source.File, ev.Source.Line, false, loopReadChars+400)
		if !strings.Contains(got, fmt.Sprintf("END-OF-EXCERPT-%d", i+1)) || !strings.Contains(got, fmt.Sprintf("excerpt %d of the build log", i+1)) {
			t.Fatalf("excerpt %d cannot be read whole from %s (%d characters)", i+1, place, len(got))
		}
	}
}

// A quiet restart carries nothing; while its dossier is written the user
// pastes a burst of log excerpts, more than the window holds. The first
// call of the new conversation, which holds them whole, is refused. On
// every size of route that is the same case: one more restart lists them
// by place, the user is answered and told nothing alarming, and the new
// briefing says plainly what was not seen and where it is.
func TestABurstDuringAPassGetsOneMoreRestartOnEveryRouteSize(t *testing.T) {
	for _, window := range []int{60_000, 128_000, 200_000, 1_000_000} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			var sent []event.Event
			s, _ := r6QuietRestart(t, window, func(s *session, pass int) {
				if pass == 1 {
					sent = r6SendBurst(s, r6Burst(window))
				}
			})
			if n := r6ToldFull(s); n != 0 {
				t.Fatalf("the user was told the conversation is full, and one more restart fits")
			}
			if n := len(s.events(event.Dossier)); n != 2 {
				t.Fatalf("%d restarts, want 2: the quiet one and the one more try", n)
			}
			if n := r5Told(s, "Noted."); n < 2 {
				t.Fatalf("the user was not answered after the burst")
			}
			r6Reachable(t, s, sent)
		})
	}
}
