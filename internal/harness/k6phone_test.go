package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
)

// TestK2AnAnswerOnALostCardIsTakenOnceWhenTheCardCannotBeChecked, one run
// further: the tap on the card was taken as a message (its question could
// not be learned) and the card marked taken; the re-ask recorded the card
// for the question, which stays open. The session is resumed again, from
// the terminal, with the questions endpoint back: planResume finds the card
// answered and takes the same tap a second time, as the answer, without
// asking whether the record already took it (phone.answer_taken).
func TestSRPhoneACardAnswerTakenAsAMessageIsNotTakenAgainAtTheNextResume(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	card := r5CardOf(t, g)
	k2LoseCard(t, g, card)
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	fp.mu.Lock()
	fp.questionsDown = true
	fp.mu.Unlock()
	// Interactive, so the run does not end on its own before the re-ask
	// has recorded the card (with no card open, a run with no terminal
	// ends at once).
	g.ui = &fakeUI{input: make(chan string, 16)}
	g.resume(Options{Interactive: true, Prompt: "blue", PromptSource: "finalechat", PromptPhoneID: fp.lastMessageID(), PromptAnswers: card})
	waitFor(t, "the re-ask to record the card", func() bool {
		return len(g.events(event.PhoneQuestion)) == 2
	})
	g.interrupt()
	if a, m := r5takenTimes(g, "blue"); a+m != 1 {
		t.Fatalf("setup: the tap is recorded %d times as the answer and %d as a message after the second run", a, m)
	}
	if integration.ReadPhoneLedger(g.log()).Cards[card] != true {
		t.Fatal("setup: the record does not hold the card as taken")
	}
	// The questions endpoint is back; the person resumes from the terminal.
	fp.mu.Lock()
	fp.questionsDown = false
	fp.mu.Unlock()
	g.ui = &fakeUI{input: make(chan string, 16)}
	g.resume(Options{Prompt: "carry on"})
	if a, m := r5takenTimes(g, "blue"); a+m != 1 {
		r5takenDump(g)
		t.Fatalf("one tap on the card is recorded %d times as the answer and %d times as a message", a, m)
	}
}

// The same rule for the words a session is resumed with: the card was
// answered while the session was stopped, a resume took the card's answer
// from the card itself (phone.answer_taken) and the run stopped before its
// poll read the card's answer message. The reply watcher then resumes the
// session with that message; its words are the card's answer the record
// holds already, and are not taken again.
func TestK6AnAnswerTakenFromItsCardIsNotTakenAgainAsResumingWords(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	card := r5CardOf(t, g)
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	tap := fp.lastMessageID()
	svc := newR13Service(t, fp)
	svc.hold = make(chan struct{}) // the poll reads nothing in this run
	t.Cleanup(func() {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		if svc.hold != nil {
			close(svc.hold)
			svc.hold = nil
		}
	})
	g.cfg.Finalechat.BaseURL = svc.srv.URL
	g.ui = &fakeUI{input: make(chan string, 16)}
	g.resume(Options{Interactive: true})
	waitFor(t, "the resume to take the card's answer", func() bool {
		return integration.ReadPhoneLedger(g.log()).Cards[card]
	})
	waitFor(t, "the thread to be attached", func() bool { return len(g.events(event.PhoneThread)) == 2 })
	g.interrupt()
	if a, m := r5takenTimes(g, "blue"); a+m != 1 {
		t.Fatalf("setup: the tap is recorded %d times as the answer and %d as a message", a, m)
	}
	g.cfg.Finalechat.BaseURL = fp.srv.URL
	g.ui = &fakeUI{input: make(chan string, 16)}
	g.resume(Options{Interactive: true, Prompt: "blue", PromptSource: "finalechat", PromptPhoneID: tap, PromptAnswers: card})
	waitFor(t, "the thread to be attached", func() bool { return len(g.events(event.PhoneThread)) == 3 })
	g.rt.sync(func() {})
	if a, m := r5takenTimes(g, "blue"); a+m != 1 {
		r5takenDump(g)
		t.Fatalf("one tap on the card is recorded %d times as the answer and %d times as a message", a, m)
	}
}
