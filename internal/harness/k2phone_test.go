package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// k2LoseCard makes the record lose the open question's card, as a stop
// between the ask's reply and its record does: its phone.question line
// names another card.
func k2LoseCard(t *testing.T, g *phoneRig, card string) {
	t.Helper()
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
}

// Fifteenth review, finding 3: the record lost the open question's card,
// the person answers on it while the session is stopped, and the reply
// watcher resumes the session with the answer; the service cannot be
// asked about the card during the resume. The words are taken once (here
// as a message, the card's question being unknown), and the re-ask that
// then finds the card answered does not take them a second time.
func TestK2AnAnswerOnALostCardIsTakenOnceWhenTheCardCannotBeChecked(t *testing.T) {
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
	g.resume(Options{Prompt: "blue", PromptSource: "finalechat", PromptPhoneID: fp.lastMessageID(), PromptAnswers: card})
	waitFor(t, "the re-ask to record the card", func() bool {
		for _, ev := range g.log() {
			var d event.PhoneQuestionData
			if ev.Type == event.PhoneQuestion && ev.Decode(&d) == nil && d.CardID == card {
				return true
			}
		}
		return false
	})
	r14Drained(t, g)
	g.rt.sync(func() {})
	if a, m := r5takenTimes(g, "blue"); a+m != 1 {
		r5takenDump(g)
		t.Fatalf("one tap on the card is recorded %d times as the answer and %d times as a message", a, m)
	}
	// No card of the question is open on the phone now: a session with no
	// terminal does not wait there for ever.
	g.exit(20 * time.Second)
}
