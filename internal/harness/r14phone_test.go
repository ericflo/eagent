package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// r14AskedAsMessage reports whether the question went to the thread as a
// message (the service refused it as a card).
func r14AskedAsMessage(fp *phoneFake, question string) bool {
	return len(fp.postsWhere(func(p map[string]any) bool {
		body, _ := p["body"].(string)
		return strings.Contains(body, question) && p["sender"] != "user"
	})) == 1
}

// r14Until waits, outside a test goroutine, until cond holds or ten
// seconds pass (the fake's hooks cannot fail the test themselves).
func r14Until(cond func() bool) {
	deadline := time.Now().Add(10 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
}

// Finding 16 of the fourteenth review: a question the service refuses as a
// card goes to the thread as a message, and the session waits for the
// reply. That message carries the waiting line as the card would: a post
// without a line clears it, and the keeper, believing the line shown,
// would leave the thread looking idle for minutes.
func TestR14AQuestionPostedAsAMessageKeepsTheWaitingLine(t *testing.T) {
	quickKeeper(t)
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	fp.onAsk = func() {
		// The keeper has shown the waiting line by the time the service
		// refuses the card.
		r14Until(func() bool { return fp.shownStatus() == "Waiting for your answer" })
	}
	g := r8AskSession(t, fp, "Which branch?", r8Choices(21))
	waitFor(t, "the question in the thread", func() bool { return r14AskedAsMessage(fp, "Which branch?") })
	g.statusIdle()
	if got := fp.shownStatus(); got != "Waiting for your answer" {
		t.Fatalf("the thread shows %q while the session waits for the answer", got)
	}
}

// Finding 16, the thread: the message that stands in for the card can
// recreate a thread the user deleted, and the session then knows the
// thread by its new id, as after any other post of its own.
func TestR14AQuestionPostedAsAMessageTakesARecreatedThread(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	svc := newR13Service(t, fp)
	fp.onAsk = func() { svc.deleteThread("t2") }
	choices := r8Choices(21)
	g := phoneStart(t, fp, "pick the branch to ship", false, func(_ string, msgs []map[string]any) reply {
		if !phoneLastIsTool(msgs) && strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(allText(msgs), "Which branch?") {
			return phoneAskAndStop("Which branch?", choices...)
		}
		return hold()
	}, func(c *config.Config) { c.Finalechat.BaseURL = svc.srv.URL })
	waitFor(t, "the question in the thread", func() bool { return r14AskedAsMessage(fp, "Which branch?") })
	waitFor(t, "the session to know the thread by its new id", func() bool {
		var p *phone
		g.rt.sync(func() { p = g.rt.phone })
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.threadID == "t2"
	})
}
