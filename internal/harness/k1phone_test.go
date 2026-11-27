package harness

import (
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// B13, on a service that keeps client keys: the resumed session asks its
// open question again, and the service answers with the earlier card
// itself. While that ask is in flight the person closes the card and
// writes; their words are not the answer to the question they dismissed.
func TestK1DismissedCardThenWordsWhileTheKeyedAskIsInFlight(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	var first event.PhoneQuestionData
	_ = r13Logged(g, event.PhoneQuestion)[0].Decode(&first)
	var once sync.Once
	var words string
	fp.mu.Lock()
	fp.onAsk = func() {
		once.Do(func() {
			fp.mu.Lock()
			q := fp.questions[first.CardID]
			q["status"], q["answered_at"] = "dismissed", time.Now().UTC().Format(time.RFC3339Nano)
			fp.cond.Broadcast()
			fp.mu.Unlock()
			words = r10Says(fp, r10Msg("never mind, check the tests first"))[0]
			r14Until(func() bool {
				for _, ev := range r13Logged(g, event.UserAnswer) {
					var d event.UserAnswerData
					if ev.Decode(&d) == nil && d.PhoneID == words {
						return true
					}
				}
				return r14Taken(g, words) > 0
			})
		})
	}
	fp.mu.Unlock()
	g.resume(Options{Interactive: true})
	waitFor(t, "the card to be recorded again", func() bool { return len(r13Logged(g, event.PhoneQuestion)) == 2 })
	r14Drained(t, g)
	g.rt.sync(func() {})
	for _, ev := range r13Logged(g, event.UserAnswer) {
		var d event.UserAnswerData
		if ev.Decode(&d) == nil && d.PhoneID == words {
			t.Fatalf("the person dismissed the card and then wrote %q; it was taken as the answer (dismissals %d)", d.Text, len(r13Logged(g, event.UserDismiss)))
		}
	}
}
