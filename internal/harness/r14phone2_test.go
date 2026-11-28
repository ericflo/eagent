package harness

import (
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// rv14e suspicion 1 (b2eb946): on a service with no client keys, the
// resumed session asks its open question again. While that ask is in
// flight the person closes the earlier card, still open on the phone, and
// writes. Their words must not become the answer to the question they
// just dismissed.
func TestR14RVEDismissedEarlierCardThenWordsWhileTheAskIsInFlight(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	fp.features = []string{"activity", "dismiss", "attachments"}
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
			// The service is slow to answer the ask: the poll reads the
			// words meanwhile.
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
	waitFor(t, "the second card to be recorded", func() bool { return len(r13Logged(g, event.PhoneQuestion)) == 2 })
	r14Drained(t, g)
	g.rt.sync(func() {})
	for _, ev := range r13Logged(g, event.UserAnswer) {
		var d event.UserAnswerData
		if ev.Decode(&d) == nil && d.PhoneID == words {
			t.Fatalf("the person dismissed the earlier card and then wrote %q; it was taken as the answer (dismissals %d)", d.Text, len(r13Logged(g, event.UserDismiss)))
		}
	}
}
