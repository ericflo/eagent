package harness

import (
	"context"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
)

// What becomes of the words a stopped session is resumed with while a
// question is open, given what the service says of the question's cards.
// This is the one place that decides it; Resume carries the plan out.

// cardState is what the service says of one card of the open question:
// its status ("pending", "answered", "dismissed", "expired", "cancelled"),
// "gone" when the service no longer has it (its thread was deleted), or
// "unknown" when it could not be asked. Answer is an answered card's
// answer, as the words the user gave.
type cardState struct {
	ID, Status, Answer string
}

// resumeWords describes the words M the session is resumed with.
type resumeWords struct {
	// AnswersCard is the card M says of itself it was written on (the
	// phone message's meta), or "".
	AnswersCard string
	// OnOpenCard: AnswersCard is one of the open question's cards.
	OnOpenCard bool
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
// card's answer answers it, and what M becomes.
type resumePlan struct {
	Dismiss    bool
	CardAnswer *cardState
	Words      wordsFate
}

// decideResume applies the rules in order; the first that matches wins.
//
//  1. A card of the question is found dismissed: the question is dismissed
//     (it decides nothing and sends nothing) and M is a plain message.
//  2. A card is found answered: its answer is the answer. If M is that
//     same answer message, M is taken as it (once, with the client details
//     it carries); else the card's answer is taken and M follows as a
//     message.
//  3. M says of itself it answers a card: one of the question's, M is the
//     answer (whether or not the service could be asked); another
//     question's, M is a message and the question stays open.
//  4. Every card's state is known and none is answered or dismissed: M is
//     the answer, wherever it came from.
//  5. Some card's state is unknown: M typed at the terminal or in the web
//     UI, or given as the answer, is the answer; M from the phone is a
//     message and the question stays open (its card may have been
//     dismissed there).
func decideResume(cards []cardState, m resumeWords) resumePlan {
	for _, c := range cards {
		if c.Status == "dismissed" {
			return resumePlan{Dismiss: true, Words: wordsMessage}
		}
	}
	for i, c := range cards {
		if c.Status == "answered" && c.Answer != "" {
			if m.AnswersCard == c.ID {
				// M is that answer message: it is the answer, taken once,
				// with what it carries (the phone's client details).
				return resumePlan{Words: wordsAnswer}
			}
			return resumePlan{CardAnswer: &cards[i], Words: wordsMessage}
		}
	}
	if m.AnswersCard != "" {
		if m.OnOpenCard {
			return resumePlan{Words: wordsAnswer}
		}
		return resumePlan{Words: wordsMessage}
	}
	for _, c := range cards {
		if c.Status == "unknown" {
			if m.FromPhone && !m.Explicit {
				return resumePlan{Words: wordsMessage}
			}
			return resumePlan{Words: wordsAnswer}
		}
	}
	return resumePlan{Words: wordsAnswer}
}

// readCards asks the service about every card of the open question that
// may still be open (liveCards). Nil when there is no question, no card,
// or no phone configured. One shared 5-second budget; what it cannot ask
// in time is "unknown". Called before the loop starts.
func (r *Runtime) readCards() []cardState {
	q := r.st.Question
	if q == nil {
		return nil
	}
	ids := liveCards(r.st, q.ID)
	fc := r.cfg.Finalechat
	if len(ids) == 0 || !fc.Wanted() || finalechat.Disabled() {
		return nil
	}
	client, ok := finalechat.Resolve(fc.TokenEnvName(), fc.BaseURL)
	if !ok {
		return nil
	}
	client.UserAgent = "eagent/" + Version
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out []cardState
	for _, id := range ids {
		got, err := client.Question(ctx, id, 0)
		switch {
		case finalechat.Gone(err):
			out = append(out, cardState{ID: id, Status: "gone"})
		case err != nil:
			out = append(out, cardState{ID: id, Status: "unknown"})
		default:
			out = append(out, cardState{ID: id, Status: got.Status, Answer: cardAnswer(got.Answer)})
		}
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
		OnOpenCard:  r.answersOpenCard(opts.PromptAnswers),
		FromPhone:   opts.PromptSource == "finalechat",
		Explicit:    opts.Answer != "",
	}
	plan := decideResume(r.readCards(), m)
	switch {
	case plan.Dismiss:
		r.append(event.New(event.UserDismiss, event.ActorUser, event.UserDismissData{QuestionID: q.ID, Source: "finalechat"}))
	case plan.CardAnswer != nil:
		// The service keeps the answer, not the phone's client details
		// (those ride on the transcript message only), so none are carried.
		r.append(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: q.ID, Text: plan.CardAnswer.Answer, Source: "finalechat"}))
	}
	return plan
}
