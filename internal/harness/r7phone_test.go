package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Finding 17: words written on the open question's card while the session
// was stopped are its answer, verbatim, as the running poll takes them: a
// leading slash runs no command, and a number is not turned into an option.
func TestACardAnswerThatResumesIsTakenVerbatim(t *testing.T) {
	for _, words := range []string{"/status", "1"} {
		t.Run(words, func(t *testing.T) {
			fp := newPhoneFake(true)
			t.Cleanup(fp.srv.Close)
			g := r5AskedAndStopped(t, fp)
			card := r5CardOf(t, g)
			if !fp.answer(words, "") {
				t.Fatal("no card to answer")
			}
			g.resume(Options{Prompt: words, PromptSource: "finalechat", PromptAnswers: card})
			waitFor(t, "the answer to be recorded", func() bool { n, open := r5Answers(g); return n == 1 && !open })
			for _, ev := range g.log() {
				var d event.UserAnswerData
				if ev.Type == event.UserAnswer && ev.Decode(&d) == nil && d.Text != words {
					t.Fatalf("the card's answer %q was recorded as %q", words, d.Text)
				}
			}
		})
	}
}
