package harness

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/store"
)

// The decision for a stopped session with a question open, resumed by
// words M, row by row.
func TestWhatResumingWordsBecomeFollowsOneTable(t *testing.T) {
	pending := cardState{ID: "c1", Status: "pending"}
	answered := cardState{ID: "c1", Status: "answered", Answer: "blue"}
	dismissed := cardState{ID: "c1", Status: "dismissed"}
	unknown := cardState{ID: "c1", Status: "unknown"}
	gone := cardState{ID: "c0", Status: "gone"}
	expired := cardState{ID: "c0", Status: "expired"}
	phone := resumeWords{FromPhone: true}
	onCard := resumeWords{FromPhone: true, AnswersCard: "c1", OnOpenCard: true}
	onOther := resumeWords{FromPhone: true, AnswersCard: "x9"}
	terminal := resumeWords{}
	explicit := resumeWords{Explicit: true}
	for _, c := range []struct {
		name    string
		cards   []cardState
		m       resumeWords
		dismiss bool
		card    string // the card whose answer is taken
		words   wordsFate
	}{
		{"dismissed: dismissal, words a message", []cardState{dismissed}, phone, true, "", wordsMessage},
		{"dismissed beats an answer on the card", []cardState{dismissed}, onCard, true, "", wordsMessage},
		{"dismissed beats --answer", []cardState{dismissed}, explicit, true, "", wordsMessage},
		{"dismissed with another card unknown", []cardState{unknown, dismissed}, phone, true, "", wordsMessage},
		{"answered: the card's answer, words a message", []cardState{answered}, phone, false, "c1", wordsMessage},
		{"answered, words are that answer: taken once", []cardState{answered}, onCard, false, "", wordsAnswer},
		{"answered, words typed at the terminal follow", []cardState{answered}, terminal, false, "c1", wordsMessage},
		{"answered, --answer follows as a message", []cardState{answered}, explicit, false, "c1", wordsMessage},
		{"answered with another card unknown", []cardState{unknown, answered}, phone, false, "c1", wordsMessage},
		{"on the open card, service down", []cardState{unknown}, onCard, false, "", wordsAnswer},
		{"on the open card, card pending", []cardState{pending}, onCard, false, "", wordsAnswer},
		{"on another question's card", []cardState{pending}, onOther, false, "", wordsMessage},
		{"on another question's card, no cards", nil, onOther, false, "", wordsMessage},
		{"known, pending: phone words answer", []cardState{pending}, phone, false, "", wordsAnswer},
		{"known, expired and gone: phone words answer", []cardState{expired, gone}, phone, false, "", wordsAnswer},
		{"no cards: phone words answer", nil, phone, false, "", wordsAnswer},
		{"no cards: terminal words answer", nil, terminal, false, "", wordsAnswer},
		{"unknown: phone words a message", []cardState{unknown}, phone, false, "", wordsMessage},
		{"unknown: terminal or web words answer", []cardState{unknown}, terminal, false, "", wordsAnswer},
		{"unknown: --answer answers", []cardState{unknown}, explicit, false, "", wordsAnswer},
		{"unknown beside a pending card: phone words a message", []cardState{pending, unknown}, phone, false, "", wordsMessage},
	} {
		got := decideResume(c.cards, c.m)
		card := ""
		if got.CardAnswer != nil {
			card = got.CardAnswer.ID
		}
		if got.Dismiss != c.dismiss || card != c.card || got.Words != c.words {
			t.Errorf("%s: dismiss=%v card=%q words=%d; want dismiss=%v card=%q words=%d", c.name, got.Dismiss, card, got.Words, c.dismiss, c.card, c.words)
		}
	}
}

// The person answers the card while the session is stopped, then writes
// a plain message, which is what resumes the session. The card's answer
// is the answer; the later words follow as a message.
func TestACardAnsweredWhileAwayIsTheAnswerWhenAPlainMessageResumes(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	g.resume(Options{Prompt: "are you still there?", PromptSource: "finalechat"})
	waitFor(t, "the answer and the message", func() bool {
		answered, message := false, false
		for _, ev := range g.log() {
			switch ev.Type {
			case event.UserAnswer:
				var d event.UserAnswerData
				if ev.Decode(&d) == nil {
					if d.Text != "blue" {
						t.Fatalf("the answer is %q; the card was answered \"blue\"", d.Text)
					}
					answered = true
				}
			case event.UserMessage:
				var d event.UserMessageData
				if ev.Decode(&d) == nil && d.Text == "are you still there?" {
					if !answered {
						t.Fatal("the message came before the card's answer")
					}
					message = true
				}
			}
		}
		return answered && message
	})
	if n, open := r5Answers(g); n != 1 || open {
		t.Fatalf("%d answers, question open=%v", n, open)
	}
}

// Words written on an earlier question's card (its card was never
// withdrawn) resume the session while a later question is open: they are
// a message, and the open question stays open, as in a running session.
func TestWordsOnAnEarlierQuestionsCardDoNotAnswerTheOpenOne(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	oldCard := r5CardOf(t, g)
	sess, _, err := store.Open(g.rt.sess.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q2", Text: "Ship it now?", Options: []string{"yes", "no"}})); err != nil {
		t.Fatal(err)
	}
	sess.Close()
	g.resume(Options{Prompt: "red", PromptSource: "finalechat", PromptAnswers: oldCard})
	waitFor(t, "the words to be recorded", func() bool {
		for _, ev := range g.log() {
			var d event.UserMessageData
			if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == "red" {
				return true
			}
		}
		return false
	})
	for _, ev := range g.log() {
		if ev.Type == event.UserAnswer {
			t.Fatalf("taken as an answer: %s", ev.Data)
		}
	}
	var open bool
	g.rt.sync(func() { open = g.rt.st.Question != nil && g.rt.st.Question.ID == "q2" })
	if !open {
		t.Fatal("q2 is not open")
	}
}

// One card's answer is taken once, whoever comes first: the poll meeting
// the answer message or a re-ask finding the card answered.
func TestACardsAnswerIsTakenOnceWhoeverComesFirst(t *testing.T) {
	p := &phone{taken: map[string]bool{}}
	if !p.takeCard("c1") || p.takeCard("c1") || !p.takeCard("c2") {
		t.Fatal("a card's answer was taken twice, or another card's not at all")
	}
	for _, c := range []struct {
		a    *finalechat.Answer
		want string
	}{
		{&finalechat.Answer{Selected: []string{"red"}}, "red"},
		{&finalechat.Answer{Selected: []string{"red", "blue"}, Text: "both"}, "red, blue — both"},
		{&finalechat.Answer{Text: " teal "}, "teal"},
		{nil, ""},
	} {
		if got := cardAnswer(c.a); got != c.want {
			t.Errorf("cardAnswer(%+v) = %q, want %q (as the service writes the answer message)", c.a, got, c.want)
		}
	}
}
