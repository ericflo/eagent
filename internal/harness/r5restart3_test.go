package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// r5QuietRestart runs a session on a small route whose first answer is
// past the restart size, so the conversation is restarted at the next
// quiet moment with nothing unseen: that restart carries nothing. While
// its pass is out, during(s) runs; then the pass is let go. It returns
// once the user has been answered "Noted." twice (the first message, and
// the one sent during the pass) or the session has been told it is full.
func r5QuietRestart(t *testing.T, during func(s *session)) *session {
	setFor(t, &errorRetryDelays, []time.Duration{5 * time.Millisecond})
	route := &r5PictureRoute{window: 60000, ceiling: 2048, picture: 80000}
	inner := route.brain()
	release := make(chan struct{})
	var passes, answers atomic.Int32
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	brain := func(model string, m []map[string]any) reply {
		if dossierRequest(m) != "" {
			if passes.Add(1) == 1 {
				<-release
			}
			return inner(model, m)
		}
		r := inner(model, m)
		if r.status == 0 && answers.Add(1) == 1 {
			r.prompt = 50000 // past the restart size: the next quiet moment restarts
		}
		return r
	}
	s := startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	})
	r5LogOnFailure(s)
	s.settled()
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "@e hello", Route: "eagent"}) })
	r4Until(t, "the pass of the quiet restart", func() bool { return passes.Load() == 1 })
	during(s)
	close(release)
	r4Until(t, "the answer, or the user told the conversation is full", func() bool {
		return r5Told(s, "Noted.") >= 2 || r5Told(s, fullNotice) > 0
	})
	if d := s.dossiers(); len(d) == 0 || d[0].CoversSeq == 0 {
		t.Fatalf("the first restart was not one that carried nothing: %+v", d)
	}
	return s
}

// A restart carries nothing; while it is written the user sends eagent a
// message with a picture the provider counts as more than the window
// holds. The first call of the new conversation, which holds the message
// whole, is refused. One more restart takes the message in by place, and
// the user is answered.
func TestAPictureSentDuringAPassThatCarriedNothingGetsOneMoreRestart(t *testing.T) {
	s := r5QuietRestart(t, func(s *session) { r5SendPicture(s, "@e what does this screenshot show?") })
	if n := r5Told(s, fullNotice); n != 0 {
		t.Fatalf("the user was told the conversation is full, and one more restart would have fit")
	}
	if n := len(s.events(event.Dossier)); n != 2 {
		t.Fatalf("%d restarts, want 2: the quiet one and the one more try", n)
	}
}

// The same with a long text message, longer than the window holds.
func TestALongMessageSentDuringAPassThatCarriedNothingGetsOneMoreRestart(t *testing.T) {
	s := r5QuietRestart(t, func(s *session) {
		long := "@e " + strings.Repeat("the heron lands at dusk on the far bank. ", 6000)
		s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: long, Route: "eagent"}) })
	})
	if n := r5Told(s, fullNotice); n != 0 {
		t.Fatalf("the user was told the conversation is full, and one more restart would have fit")
	}
	if n := len(s.events(event.Dossier)); n != 2 {
		t.Fatalf("%d restarts, want 2: the quiet one and the one more try", n)
	}
}
