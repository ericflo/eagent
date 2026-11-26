package harness

import (
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// The person answers the open question's card while the session is
// stopped, and the reply watcher resumes the session with that answer,
// whose meta names a card the record holds. The process that resumes it
// cannot ask the phone service at all (here EAGENT_FINALECHAT=off). The
// record says whose card it is: the words are the answer, as at 80f4746.
func TestAnAnswerOnARecordedCardIsTheAnswerWhenTheServiceCannotBeAsked(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	card := r5CardOf(t, g)
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	t.Setenv("EAGENT_FINALECHAT", "off")
	g.resume(Options{Prompt: "blue", PromptSource: "finalechat", PromptPhoneID: fp.lastMessageID(), PromptAnswers: card})
	waitFor(t, "the words to be recorded", func() bool { a, m := r5takenTimes(g, "blue"); return a+m > 0 })
	if a, m := r5takenTimes(g, "blue"); a != 1 || m != 0 {
		r5takenDump(g)
		t.Fatalf("the answer written on the open question's card is recorded %d times as the answer and %d times as a message", a, m)
	}
}

// r5Seqs is the record's sequence numbers of the events of type ty.
func r5Seqs(g *phoneRig, ty string) []int64 {
	var out []int64
	for _, ev := range g.log() {
		if ev.Type == ty {
			out = append(out, ev.Seq)
		}
	}
	return out
}

// In a running session the person answers the card on the phone. The
// record says the card's answer was taken only after the answer itself is
// in it, never before: a stop between the two leaves an answer, not a
// claim with nothing behind it.
func TestACardAnswerIsRecordedBeforeItsClaim(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "paint it", true, r5AskBrain(), nil)
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	waitFor(t, "the card to be watched", func() bool { return len(g.events(event.PhoneQuestion)) > 0 })
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	waitFor(t, "the answer and its claim", func() bool {
		return len(r5Seqs(g, event.UserAnswer)) == 1 && len(r5Seqs(g, event.PhoneAnswerTaken)) == 1
	})
	if a, c := r5Seqs(g, event.UserAnswer)[0], r5Seqs(g, event.PhoneAnswerTaken)[0]; c < a {
		r5takenDump(g)
		t.Fatalf("the claim (seq %d) was recorded before the answer (seq %d)", c, a)
	}
}

// The re-ask finds the card answered, but meanwhile the question was
// answered at the terminal. The card's answer is not dropped: it is kept
// as a message, as the poll keeps an answer to a question no longer open.
func TestACardAnswerTheReaskFindsAfterAnAnswerElsewhereIsKeptAsAMessage(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	// The resume cannot check the card (rule 5: the phone words are a
	// message and the question stays open); the re-ask then finds it.
	fp.mu.Lock()
	fp.questionsDown = true
	var once sync.Once
	fp.onAsk = func() {
		once.Do(func() {
			// While the re-ask is in flight, the person answers at the terminal.
			g.ui.input <- "green"
			waitFor(t, "the terminal answer", func() bool {
				for _, ev := range g.log() {
					var d event.UserAnswerData
					if ev.Type == event.UserAnswer && ev.Decode(&d) == nil && d.Text == "green" {
						return true
					}
				}
				return false
			})
		})
	}
	fp.mu.Unlock()
	g.ui = &fakeUI{input: make(chan string, 16)}
	g.resume(Options{Prompt: "are you there?", PromptSource: "finalechat", Interactive: true})
	waitFor(t, "the card's answer kept as a message", func() bool { _, m := r5takenTimes(g, "blue"); return m == 1 })
	if a, _ := r5takenTimes(g, "blue"); a != 0 {
		t.Fatalf("the card's answer was also taken as an answer")
	}
	waitFor(t, "its claim", func() bool { return len(r5Seqs(g, event.PhoneAnswerTaken)) == 1 })
}
