package harness

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// The web page, for a session a terminal runs, while that run ends (the
// user typed /quit there): the user presses Dismiss on the open question
// and then writes a message. Both files land in the inbox after the loop's
// last look, so the run's last look (finish) reads them. It leaves the
// dismissal for the next run (9c1f8f8) but takes the message, with the
// question still open, as the ANSWER to the question the user dismissed.
// On main the run's end read nothing: the next run took the dismissal
// (only a stale stop is ignored), then the message, as a message.
func TestSR3WebAMessageAfterADismissalAsTheRunEndsBecomesTheAnswer(t *testing.T) {
	setFor(t, &loopPoll, time.Hour) // the loop reads its inbox at its start only
	asked := false
	g := phoneStart(t, nil, "merge the PRs", true, func(_ string, msgs []map[string]any) reply {
		if !asked && !phoneLastIsTool(msgs) {
			asked = true
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge #45 as well?","options":["yes","no"]}`)}}
		}
		return hold()
	}, nil)
	var qid string
	waitFor(t, "the question to be open", func() bool {
		g.rt.sync(func() {
			if q := g.rt.st.Question; q != nil {
				qid = q.ID
			}
		})
		return qid != ""
	})
	g.rt.sync(func() {
		// On the loop: the page's two posts, then the terminal's /quit,
		// before the loop looks at its inbox again.
		if err := PostInbox(g.rt.sess.Path, InboxMessage{Type: "dismiss", QuestionID: qid, From: "web"}); err != nil {
			t.Error(err)
		}
		if err := PostInbox(g.rt.sess.Path, InboxMessage{Type: "message", Text: "skip that and bump the version", From: "web"}); err != nil {
			t.Error(err)
		}
		g.rt.beginShutdown("quit", 0)
	})
	g.exit(10 * time.Second)
	for _, ev := range g.log() {
		var d event.UserAnswerData
		if ev.Type == event.UserAnswer && ev.Decode(&d) == nil && d.QuestionID == qid {
			t.Fatalf("the user dismissed question %s and then wrote %q; the record takes the message as the question's answer (seq %d), and the dismissal waits in the inbox (%d file(s))", qid, d.Text, ev.Seq, k5Inbox(g))
		}
	}
}
