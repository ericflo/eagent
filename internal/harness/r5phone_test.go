package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// r5AskBrain asks one question, stops the agent, and says "Done." once the
// question is answered and the session is closing.
func r5AskBrain() func(string, []map[string]any) reply {
	return func(_ string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			if strings.Contains(all, "Done.") {
				return hold()
			}
			return say("Done.")
		case strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(all, "Which colour?"):
			return phoneAskAndStop("Which colour?", "red", "blue")
		}
		return hold()
	}
}

// r5AskedAndStopped starts a batch session that asks its question on the
// phone, waits for the card to be recorded, and stops the session with the
// question still open.
func r5AskedAndStopped(t *testing.T, fp *phoneFake) *phoneRig {
	t.Helper()
	g := phoneStart(t, fp, "paint it", false, r5AskBrain(), nil)
	waitFor(t, "the first phone question", func() bool { return fp.pendingQuestions() == 1 })
	waitFor(t, "the card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) > 0 })
	g.interrupt()
	return g
}

// The service answers a question's client key with the question it made
// for that key, whatever its status. A question whose card timed out while
// the session was stopped must come back as a new card on resume, not as
// the dead one, which would end the session at once.
func TestResumedQuestionWhoseCardExpiredIsAskedOnANewCard(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	if fp.expire() != 1 {
		t.Fatal("no card to expire")
	}
	g.resume(Options{})
	waitFor(t, "a new card for the question", func() bool { return fp.pendingQuestions() == 1 })
	if n := fp.cards(); n != 2 {
		t.Fatalf("cards = %d, want the expired one and a new one", n)
	}
	if !fp.answer("red", "") {
		t.Fatal("no pending question")
	}
	if code := g.exit(30 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	if st := state.Replay(g.log()); st.EndReason != "done" || st.Question != nil {
		t.Fatalf("end=%s question=%v", st.EndReason, st.Question)
	}
}

// A card still open on the service when the session resumes is the one
// the user answers: the re-ask must not put up a second card.
func TestResumedQuestionWhoseCardIsStillOpenKeepsThatOneCard(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	g.resume(Options{})
	waitFor(t, "the question to be asked again", func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return len(fp.asks) == 2 })
	if n := fp.cards(); n != 1 {
		t.Fatalf("cards = %d, want the one still open", n)
	}
	if !fp.answer("red", "") {
		t.Fatal("no pending question")
	}
	if code := g.exit(30 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	if n := fp.cards(); n != 1 {
		t.Fatalf("cards = %d after the answer", n)
	}
}

// A card the user dismissed while the session was stopped stays
// dismissed: no new card, nothing decided and nothing sent to the agent.
func TestResumedQuestionWhoseCardWasDismissedIsNotAskedAgain(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	sent := len(g.seat.sentTexts())
	if !fp.dismiss() {
		t.Fatal("nothing to dismiss")
	}
	g.resume(Options{})
	code := g.exit(30 * time.Second)
	if n := fp.cards(); n != 1 {
		t.Fatalf("cards = %d, want only the dismissed one", n)
	}
	if got := len(g.seat.sentTexts()); got != sent {
		t.Fatalf("the agent was sent %d more messages", got-sent)
	}
	st := state.Replay(g.log())
	if code != 2 || st.EndReason != "awaiting-input" {
		t.Fatalf("exit %d, end reason %q; want 2 and awaiting-input", code, st.EndReason)
	}
	for _, ev := range g.log() {
		if ev.Type == event.UserAnswer {
			t.Fatalf("a dismissal recorded an answer: %s", ev.Data)
		}
	}
}
