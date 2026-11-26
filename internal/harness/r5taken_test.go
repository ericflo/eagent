package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// r5takenCaughtUp waits until the message poll has read every user
// message of the thread and the loop has run what it handed over.
func r5takenCaughtUp(g *phoneRig) {
	g.t.Helper()
	waitFor(g.t, "the poll to read the whole thread", func() bool {
		g.fp.mu.Lock()
		defer g.fp.mu.Unlock()
		last := ""
		for _, m := range g.fp.messages {
			if m["sender"] == "user" && m["origin"] != "token" {
				last = m["id"].(string)
			}
		}
		return g.fp.after == last
	})
	g.rt.sync(func() {})
}

// r5takenDump logs what the person's words became, in record order.
func r5takenDump(g *phoneRig) {
	for _, ev := range g.log() {
		switch ev.Type {
		case event.UserAnswer, event.UserMessage, event.UserDismiss, event.PhoneQuestion:
			g.t.Logf("%d %s %s", ev.Seq, ev.Type, ev.Data)
		}
	}
}

// r5takenTimes counts the records that hold text: as an answer, as a
// message.
func r5takenTimes(g *phoneRig, text string) (answers, messages int) {
	for _, ev := range g.log() {
		switch ev.Type {
		case event.UserAnswer:
			var d event.UserAnswerData
			if ev.Decode(&d) == nil && d.Text == text {
				answers++
			}
		case event.UserMessage:
			var d event.UserMessageData
			if ev.Decode(&d) == nil && d.Text == text {
				messages++
			}
		}
	}
	return answers, messages
}

// The person writes a plain message on the phone, which wakes the stopped
// session, and taps an option on the card a moment later, before the
// resume reads the cards. The resume takes the card's answer from the
// service; the poll, which reads on from the message that resumed the
// session, then meets the card's answer message and must not take it
// again.
func TestACardAnswerTakenByTheResumeIsNotTakenAgainByThePoll(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	fp.userReply("wait, one thing")
	mid := fp.lastMessageID()
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	g.resume(Options{Prompt: "wait, one thing", PromptSource: "finalechat", PromptPhoneID: mid})
	waitFor(t, "the card's answer", func() bool { a, _ := r5takenTimes(g, "blue"); return a == 1 })
	r5takenCaughtUp(g)
	if a, m := r5takenTimes(g, "blue"); a != 1 || m != 0 {
		r5takenDump(g)
		t.Fatalf("the card's one answer \"blue\" is recorded %d times as the answer and %d times as a message", a, m)
	}
}

// The record lost the card of the open question (the session stopped
// between the ask's reply and its record). The person answers on that
// card; the reply watcher resumes the session with the answer, whose meta
// names the card. The card's own meta says it is the open question's, so
// the words are its answer, once; the re-ask then finds the card answered
// and must not take it again.
func TestAnAnswerOnACardTheRecordLostIsTakenOnce(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	card := r5CardOf(t, g)
	// The record loses the card: its phone.question line names another.
	files, _ := filepath.Glob(filepath.Join(g.rt.sess.Path, "*.jsonl"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(raw), "\n")
		for i, l := range lines {
			if strings.Contains(l, `"phone.question"`) {
				lines[i] = strings.ReplaceAll(l, card, "lost-card")
			}
		}
		if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !fp.answer("blue", "") {
		t.Fatal("no card to answer")
	}
	g.resume(Options{Prompt: "blue", PromptSource: "finalechat", PromptPhoneID: fp.lastMessageID(), PromptAnswers: card})
	waitFor(t, "the words to be recorded", func() bool { a, m := r5takenTimes(g, "blue"); return a+m > 0 })
	// Let the re-ask run: it records the card it finds.
	waitFor(t, "the re-ask", func() bool {
		for _, ev := range g.log() {
			var d event.PhoneQuestionData
			if ev.Type == event.PhoneQuestion && ev.Decode(&d) == nil && d.CardID == card {
				return true
			}
		}
		a, _ := r5takenTimes(g, "blue")
		return a > 0
	})
	g.rt.sync(func() {})
	r5takenCaughtUp(g)
	if a, m := r5takenTimes(g, "blue"); a+m != 1 {
		r5takenDump(g)
		t.Fatalf("one tap on the card is recorded %d times as the answer and %d times as a message", a, m)
	}
}

// The question has a card in another thread too. When the resume closes
// the question from the cards, the other card is withdrawn on the phone,
// as it is when the resuming words answer it.
func TestAQuestionTheResumeClosesFromItsCardsLeavesNoOtherCardOpen(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	qid := r5OpenQuestion(t, g)
	first := r5CardOf(t, g)
	// A second card of the question, in another thread, recorded after the
	// first; the person answers that one.
	fp.injectQuestion("other-card", qid)
	sess, _, err := store.Open(g.rt.sess.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.PhoneQuestion, event.ActorHarness, event.PhoneQuestionData{QuestionID: qid, CardID: "other-card", ThreadID: "t2", Key: 1})); err != nil {
		t.Fatal(err)
	}
	sess.Close()
	fp.answerQuestion("other-card", "blue")
	g.resume(Options{Prompt: "are you there?", PromptSource: "finalechat"})
	waitFor(t, "the question to close with the card's answer", func() bool { a, _ := r5takenTimes(g, "blue"); return a == 1 })
	waitFor(t, "the first card to be withdrawn", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		return fp.questions[first]["status"] != "pending"
	})
}
