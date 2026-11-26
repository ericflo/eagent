package harness

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// The rule a turn's end settles the debt by (turnEnding.settle), row by
// row: how the turn ended, by what happened in it.
func TestATurnsEndSettlesWhatIsOwedByOneRule(t *testing.T) {
	type row struct {
		name string
		e    turnEnding
		want turnSettle
	}
	var rows []row
	add := func(name string, e turnEnding, replied, cut turnSettle) {
		rows = append(rows, row{name + ", replied or held", e, replied})
		c := e
		c.cut = true
		rows = append(rows, row{name + ", cut off", c, cut})
	}
	// The message: answered or not.
	add("message answered", turnEnding{held: true, heldSeen: true, handled: true}, clearDebt, clearDebt)
	add("message not answered, seen by a call that came back", turnEnding{held: true, heldSeen: true}, oweMessage, oweMessage)
	add("message not answered, only the failed call saw it", turnEnding{held: true}, oweMessage, keepDebt)
	add("reminder about a message not answered", turnEnding{held: true, heldSeen: true, heldOwed: true}, oweMessage, keepDebt)
	// The message not answered and an answer pending beside it.
	add("message not answered, answer pending", turnEnding{held: true, heldSeen: true, answer: true, answerSeen: true}, oweBoth, oweBoth)
	add("message not answered, answer pending, the message already the debt", turnEnding{held: true, heldSeen: true, heldOwed: true, answer: true, answerSeen: true}, oweBoth, keepDebt)
	add("message not answered, answer sent back once", turnEnding{held: true, heldSeen: true, answer: true, answerSeen: true, remindedAnswer: true}, oweMessage, oweBoth)
	// The answer acted on; a message came after that and was not answered
	// (its arrival reset what the turn had done, heldSince).
	add("answer acted on, a later message not answered", turnEnding{held: true, heldSeen: true, answer: true, answerSeen: true, answerDealt: true}, oweMessage, oweMessage)
	// A mid-turn answer, after the message was answered.
	add("message answered, answer dealt with", turnEnding{held: true, heldSeen: true, handled: true, answer: true, answerDealt: true, answerSeen: true}, clearDebt, clearDebt)
	add("message answered, answer seen by a call that came back, not dealt with", turnEnding{held: true, heldSeen: true, handled: true, answer: true, answerSeen: true}, oweTheAnswer, oweTheAnswer)
	add("message answered, answer shown only by the failed call", turnEnding{held: true, heldSeen: true, handled: true, answer: true}, clearDebt, clearDebt)
	add("message answered, answer sent back once, then held on or cut off", turnEnding{held: true, heldSeen: true, handled: true, answer: true, answerSeen: true, remindedAnswer: true}, clearDebt, oweTheAnswer)
	// An answer with no earlier debt and nothing done.
	add("answer seen, nothing done, no earlier debt", turnEnding{answer: true, answerSeen: true}, oweTheAnswer, oweTheAnswer)
	add("answer shown only by the failed call, no earlier debt", turnEnding{answer: true}, keepDebt, keepDebt)
	add("answer sent back once, then held on or cut off", turnEnding{answer: true, answerSeen: true, remindedAnswer: true}, keepDebt, oweTheAnswer)
	// The reminder turn for an earlier answer.
	add("answer's reminder, held on", turnEnding{answerDebt: true, remindedAnswer: true}, clearDebt, keepDebt)
	// Nothing for the user at all (a look while the agent works).
	add("nothing asked, nothing done", turnEnding{}, keepDebt, keepDebt)
	add("nothing asked, something done", turnEnding{handled: true}, clearDebt, clearDebt)
	for _, r := range rows {
		if got := r.e.settle(); got != r.want {
			t.Errorf("%s: %s, want %s", r.name, settleName(got), settleName(r.want))
		}
	}
}

// The user answers eagent's question and, in the same wake, writes it a
// message. The one call of the turn (max_calls_per_turn 1) holds on both.
// The message's reminder names the answer too, and once that reminder is
// answered nothing more is owed: each is brought up once, neither dropped.
func TestAnAnswerHeldOnBesideAnUnansweredMessageIsBroughtUpWithIt(t *testing.T) {
	setFor(t, &owedRetryDelays, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond})
	const answerPhrase = "nothing has been done with the answer"
	var mu sync.Mutex
	phase := 0
	var tails []string
	var reminder string
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		tail := lastUserText(msgs)
		if msgs[len(msgs)-1]["role"] != "tool" {
			tails = append(tails, tail)
		}
		switch {
		case phase == 0 && strings.Contains(tail, "kept for you"):
			phase = 1
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge #45 as well?","options":["yes","no"]}`)}}
		case phase == 1:
			// The turn that shows the answer and the message: it holds.
			phase = 2
		case phase == 2 && strings.Contains(tail, owedPhrase):
			phase, reminder = 3, tail
			r := say("Codex is idle; I will merge #45 too.")
			r.calls = append(r.calls, tc("relay_send", `{"text":"Merge #45 as well."}`))
			return r
		}
		return hold()
	}, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 1 })
	s.type_("@e should I merge #44?")
	s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
	s.settled()
	// Both in one step of the loop: one wake.
	s.rt.sync(func() {
		s.rt.onInput("yes")
		s.rt.onInput("@e and what is Codex up to?")
	})
	s.waitFor("the reminder to be answered", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return phase == 3
	})
	s.settled()
	var owed int64
	s.rt.sync(func() { owed = s.rt.turns.owed })
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(reminder, "what is Codex up to") || !strings.Contains(reminder, answerPhrase) || !strings.Contains(reminder, `"yes"`) {
		t.Fatalf("the message's reminder does not name both the message and the answer:\n%s", reminder)
	}
	n := 0
	for _, tl := range tails {
		if strings.Contains(tl, answerPhrase) {
			n++
		}
	}
	if n != 1 || owed != 0 {
		t.Fatalf("the answer was brought up %d times (want 1), and %d is still owed; tails:\n%s", n, owed, strings.Join(tails, "\n---\n"))
	}
}

func settleName(s turnSettle) string {
	switch s {
	case keepDebt:
		return "keep"
	case clearDebt:
		return "clear"
	case oweMessage:
		return "owe the message"
	case oweTheAnswer:
		return "owe the answer"
	case oweBoth:
		return "owe both"
	}
	return fmt.Sprint(int(s))
}

// A turn answers a message that was owed, asks a question and looks
// something up; the user answers while the look is out. The next call
// shows the answer and only looks again, and comes back; the call after it
// fails. That look is spent and the turn did nothing with the answer: it
// must still be brought up, not left reaching nobody.
func TestAnAnswerSeenByALookBeforeTheTurnFailsIsStillBroughtUp(t *testing.T) {
	setFor(t, &owedRetryDelays, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond})
	setFor(t, &errorRetryDelays, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond})
	const owedMark = "has had no answer and seen nothing done about it"
	var mu sync.Mutex
	phase := 0
	var afterFail []string
	gate := make(chan struct{})
	called := make(chan struct{}, 1)
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		tail := lastUserText(msgs)
		afterTool := msgs[len(msgs)-1]["role"] == "tool"
		if phase >= 6 && !afterTool {
			afterFail = append(afterFail, tail)
		}
		switch {
		case phase == 0 && strings.Contains(tail, "kept for you"):
			phase = 1
			return hold()
		case phase == 1:
			phase = 2
			return hold()
		case phase == 2 && strings.Contains(tail, owedMark):
			phase = 3
			r := say("Codex is idle; nothing is running.")
			r.calls = append(r.calls,
				tc("ask_user", `{"text":"Merge #45 as well?","options":["yes","no"]}`),
				tc("session_search", `{"query":"#45"}`),
			)
			return r
		case phase == 3 && afterTool:
			phase = 4
			select {
			case called <- struct{}{}:
			default:
			}
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"merge"}`)}, block: gate}
		case phase == 4 && afterTool:
			// This call shows the answer; it only looks, and is recorded.
			phase = 5
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"yes"}`)}}
		case phase == 5 && afterTool:
			phase = 6
			return reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}
		}
		return hold()
	}, nil)
	s.type_("@e what is Codex up to?")
	<-called
	s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
	s.type_("yes")
	s.waitFor("the answer", func() bool { return len(s.events(event.UserAnswer)) == 1 })
	close(gate)
	s.waitFor("the answer to be brought up again", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, tail := range afterFail {
			if strings.Contains(tail, "nothing has been done with the answer") {
				return true
			}
		}
		return false
	})
	s.settled()
	mu.Lock()
	defer mu.Unlock()
	for _, tail := range afterFail {
		if strings.Contains(tail, owedMark) {
			t.Fatalf("the message already answered is brought up again as unanswered:\n%s", tail)
		}
	}
}
