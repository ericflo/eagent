package harness

import (
	"strconv"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// r5Answers counts the answers the record holds and reports whether the
// question is still open.
func r5Answers(g *phoneRig) (answers int, open bool) {
	for _, ev := range g.log() {
		if ev.Type == event.UserAnswer {
			answers++
		}
	}
	g.rt.sync(func() { open = g.rt.st.Question != nil })
	return answers, open
}

// r5CardOf is the card the record holds for the open question.
func r5CardOf(t *testing.T, g *phoneRig) string {
	t.Helper()
	for _, ev := range g.log() {
		var d event.PhoneQuestionData
		if ev.Type == event.PhoneQuestion && ev.Decode(&d) == nil {
			return d.CardID
		}
	}
	t.Fatal("no card recorded")
	return ""
}

// The person answers the card while the session is stopped, and the reply
// watcher resumes the session with that answer, whose message says which
// card it answers. That is the answer, even when the card cannot be
// checked at resume: it was written on the card, so it is no dismissal.
func TestAnAnswerWrittenOnTheCardIsTakenWhenTheCardCannotBeChecked(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	card := r5CardOf(t, g)
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	fp.mu.Lock()
	fp.questionsDown = true
	fp.mu.Unlock()
	g.resume(Options{Prompt: "blue", PromptSource: "finalechat", PromptAnswers: card})
	waitFor(t, "the answer to be recorded", func() bool { n, open := r5Answers(g); return n == 1 && !open })
	for _, ev := range g.log() {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == "blue" {
			t.Fatalf("the answer was also recorded as a message: %s", ev.Data)
		}
	}
}

// When the resuming message does not say it answers the card and the card
// cannot be checked, the words stay a message; the re-ask then finds the
// card answered on the phone, and the question closes with that answer
// rather than staying open for whatever the person writes next.
func TestACardFoundAnsweredOnTheReaskClosesItsQuestion(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	fp.mu.Lock()
	fp.questionsDown = true
	fp.mu.Unlock()
	g.resume(Options{Prompt: "blue", PromptSource: "finalechat"})
	fp.mu.Lock()
	fp.questionsDown = false
	fp.mu.Unlock()
	waitFor(t, "the question to close with the card's answer", func() bool { n, open := r5Answers(g); return n == 1 && !open })
	if n := fp.pendingQuestions(); n != 0 {
		t.Fatalf("%d cards open on the phone", n)
	}
	for _, ev := range g.log() {
		var d event.UserAnswerData
		if ev.Type == event.UserAnswer && ev.Decode(&d) == nil && d.Text != "blue" {
			t.Fatalf("answer = %q", d.Text)
		}
	}
}

// A question whose card died fifty times in one thread still gets a card:
// the walk starts at the newest key the record knows in that thread, and
// its bound counts from there.
func TestAQuestionWhoseCardDiedFiftyTimesStillGetsACard(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	qid := r5OpenQuestion(t, g)
	if fp.expire() != 1 {
		t.Fatal("no card to expire")
	}
	// Cards 2..50 of earlier resumes the record lost, each expired.
	fp.mu.Lock()
	for n := 2; n <= 50; n++ {
		id := fp.nextID()
		fp.questions[id] = map[string]any{"id": id, "thread_id": "t1", "prompt": "Which colour?", "status": "expired", "allow_freeform": true, "multi_select": false, "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
		fp.qkeys["eagent:"+g.rt.sess.ID+":q:"+qid+":"+strconv.Itoa(n)] = id
	}
	fp.mu.Unlock()
	g.resume(Options{})
	waitFor(t, "a card for the open question", func() bool { return fp.pendingQuestions() == 1 })
}

// A question re-asked over many resumes costs each resume a request or
// two, however many cards it has had: the walk starts at the newest card
// the record knows in the thread.
func TestAReaskCostsAFewRequestsHoweverOftenItWasAsked(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	for round := 2; round <= 5; round++ {
		if fp.expire() != 1 {
			t.Fatalf("round %d: no card to expire", round)
		}
		fp.mu.Lock()
		before := len(fp.asks)
		fp.mu.Unlock()
		g.resume(Options{})
		waitFor(t, "a new card", func() bool { return fp.pendingQuestions() == 1 })
		waitFor(t, "the new card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) == round })
		fp.mu.Lock()
		asks := len(fp.asks) - before
		fp.mu.Unlock()
		if asks > 2 {
			t.Fatalf("round %d: the re-ask took %d requests", round, asks)
		}
		g.interrupt()
	}
}
