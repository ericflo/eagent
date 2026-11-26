package harness

import (
	"context"
	"slices"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
)

// What becomes of the words a stopped session is resumed with while a
// question is open, given what the service says of the question's cards.
// This is the one place that decides it; Resume carries the plan out.

// cardState is what the service says of one card: its status
// ("pending", "answered", "dismissed", "expired", "cancelled"), "gone" when
// the service no longer has it (its thread was deleted), or "unknown" when
// it could not be asked. Answer is an answered card's answer, as the words
// the user gave; At is when it was answered or dismissed. OfOpen says the
// card is one of the open question's: every card the record holds for it,
// and the card the words name when the card's own meta says so.
type cardState struct {
	ID, Status, Answer string
	At                 time.Time
	OfOpen             bool
}

// resumeWords describes the words M the session is resumed with.
type resumeWords struct {
	// AnswersCard is the card M says of itself it was written on (the
	// phone message's meta), or "".
	AnswersCard string
	// FromPhone: M came from the phone; otherwise it was typed at the
	// terminal or in the web UI.
	FromPhone bool
	// Explicit: M was given as the answer (--answer, the web's answer).
	Explicit bool
}

// wordsFate is what M becomes.
type wordsFate int

const (
	wordsAnswer  wordsFate = iota // the answer to the open question, as at 80f4746
	wordsMessage                  // a plain message (the question stays as the plan leaves it)
)

// resumePlan is the decision: whether the question is dismissed, which
// card's answer is taken (as the answer, or as a message when a later
// dismissal decided the question), and what M becomes.
type resumePlan struct {
	Dismiss       bool
	CardAnswer    *cardState
	CardAsMessage bool
	Words         wordsFate
	// WordsCard is the card the words were written on, to be marked taken
	// once the words are recorded ("" when whose card it is is unknown).
	WordsCard string
}

// decideResume applies the rules in order; the first that matches wins.
// Only the open question's cards (OfOpen) decide rules 1, 2 and 4.
//
//  1. A card is found dismissed, and no card answered after it: the
//     question is dismissed (it decides nothing and sends nothing) and M
//     is a plain message. A card answered before that dismissal is not
//     lost: its answer follows the dismissal as a plain message (unless M
//     is that answer message, which is the message then).
//  2. A card is found answered (after any dismissal): its answer is the
//     answer. If M is that same answer message, M is taken as it (once,
//     with the client details it carries); else the card's answer is taken
//     and M follows as a message.
//  3. M says of itself it was written on a card: one of the question's, M
//     is the answer (whether or not the service could be asked); any other
//     card, or one whose question could not be learned, M is a message and
//     the question stays open.
//  4. Every card's state is known and none is answered or dismissed: M is
//     the answer, wherever it came from.
//  5. Some card's state is unknown: M typed at the terminal or in the web
//     UI, or given as the answer, is the answer; M from the phone is a
//     message and the question stays open (its card may have been
//     dismissed there).
func decideResume(cards []cardState, m resumeWords) resumePlan {
	var dismissed, answered *cardState
	for i := range cards {
		c := &cards[i]
		if !c.OfOpen {
			continue
		}
		if c.Status == "dismissed" && (dismissed == nil || c.At.After(dismissed.At)) {
			dismissed = c
		}
		if c.Status == "answered" && c.Answer != "" && (answered == nil || c.At.After(answered.At)) {
			answered = c
		}
	}
	if dismissed != nil && (answered == nil || !answered.At.After(dismissed.At)) {
		p := resumePlan{Dismiss: true, Words: wordsMessage}
		if answered != nil && m.AnswersCard != answered.ID {
			p.CardAnswer, p.CardAsMessage = answered, true
		}
		return p
	}
	if answered != nil {
		if m.AnswersCard == answered.ID {
			// M is that answer message: it is the answer, taken once,
			// with what it carries (the phone's client details).
			return resumePlan{Words: wordsAnswer}
		}
		return resumePlan{CardAnswer: answered, Words: wordsMessage}
	}
	if m.AnswersCard != "" {
		for _, c := range cards {
			if c.ID == m.AnswersCard && c.OfOpen {
				return resumePlan{Words: wordsAnswer}
			}
		}
		return resumePlan{Words: wordsMessage}
	}
	for _, c := range cards {
		if c.OfOpen && c.Status == "unknown" {
			if m.FromPhone && !m.Explicit {
				return resumePlan{Words: wordsMessage}
			}
			return resumePlan{Words: wordsAnswer}
		}
	}
	return resumePlan{Words: wordsAnswer}
}

// readCards asks the service about every card of the open question that
// may still be open (liveCards), and about the card the words name when
// the record does not hold it (a stop between an ask and its record): its
// own meta says whose it is. Nil when there is no question or no card. When
// the service cannot be asked at all, every card is "unknown". One shared
// 5-second budget; what it cannot ask in time is "unknown". Called before
// the loop starts.
func (r *Runtime) readCards(named string) []cardState {
	q := r.st.Question
	if q == nil {
		return nil
	}
	ids := liveCards(r.st, q.ID)
	ofOpen := map[string]bool{}
	for _, id := range ids {
		ofOpen[id] = true
	}
	if named != "" && r.answersOpenCard(named) {
		ofOpen[named] = true
		if !slices.Contains(ids, named) {
			ids = append(ids, named)
		}
	} else if named != "" {
		ids = append(ids, named)
	}
	if len(ids) == 0 {
		return nil
	}
	// A service that cannot be asked at all (no phone in this process's
	// configuration, the kill switch, no token) leaves every card's state
	// unknown; whose each card is still follows from the record.
	cantAsk := func() []cardState {
		out := make([]cardState, 0, len(ids))
		for _, id := range ids {
			out = append(out, cardState{ID: id, Status: "unknown", OfOpen: ofOpen[id]})
		}
		return out
	}
	fc := r.cfg.Finalechat
	if !fc.Wanted() || finalechat.Disabled() {
		return cantAsk()
	}
	client, ok := finalechat.Resolve(fc.TokenEnvName(), fc.BaseURL)
	if !ok {
		return cantAsk()
	}
	client.UserAgent = "eagent/" + Version
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out []cardState
	for _, id := range ids {
		got, err := client.Question(ctx, id, 0)
		c := cardState{ID: id, OfOpen: ofOpen[id]}
		switch {
		case finalechat.Gone(err):
			c.Status = "gone"
		case err != nil:
			c.Status = "unknown"
		default:
			c.Status, c.Answer = got.Status, cardAnswer(got.Answer)
			if got.AnsweredAt != nil {
				c.At = *got.AnsweredAt
			}
			if qid, _ := got.Meta["question_id"].(string); qid == q.ID {
				c.OfOpen = true
			}
		}
		out = append(out, c)
	}
	return out
}

// planResume reads the open question's cards, decides what the resuming
// words become, and records what the cards decided: a dismissal, or the
// answer an answered card holds. The words themselves are left to Resume.
// Called before the loop starts.
func (r *Runtime) planResume(opts Options) resumePlan {
	q := r.st.Question
	if q == nil {
		return resumePlan{Words: wordsAnswer}
	}
	m := resumeWords{
		AnswersCard: opts.PromptAnswers,
		FromPhone:   opts.PromptSource == "finalechat",
		Explicit:    opts.Answer != "",
	}
	cards := r.readCards(opts.PromptAnswers)
	plan := decideResume(cards, m)
	if plan.Dismiss {
		r.append(event.New(event.UserDismiss, event.ActorUser, event.UserDismissData{QuestionID: q.ID, Source: "finalechat"}))
	}
	if c := plan.CardAnswer; c != nil {
		// The service keeps the answer, not the phone's client details
		// (those ride on the transcript message only), so none are carried.
		if plan.CardAsMessage {
			r.userMessage(event.UserMessageData{Text: c.Answer, Source: "finalechat"})
		} else {
			r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: c.Answer, Source: "finalechat"}))
		}
		r.markAnswerTaken(c.ID)
	}
	// The words are recorded next, as the answer or as a message; once they
	// are, the card they were written on is taken (Resume marks it), unless
	// whose card it is could not be learned (then a re-ask that finds it
	// the open question's, answered, closes the question with it).
	if card := opts.PromptAnswers; card != "" {
		for _, c := range cards {
			if c.ID == card && (c.Status != "unknown" || c.OfOpen) {
				plan.WordsCard = card
			}
		}
	}
	return plan
}

// markAnswerTaken records that a card's answer has reached the session,
// so neither the message poll nor a re-ask takes it again, in this run or
// a later one (the phone reads the record when it starts). Loop goroutine
// or before the loop starts.
func (r *Runtime) markAnswerTaken(card string) {
	r.append(event.New(event.PhoneAnswerTaken, event.ActorHarness, event.PhoneAnswerTakenData{CardID: card}))
	if r.phone != nil {
		r.phone.claim(card)
	}
}
