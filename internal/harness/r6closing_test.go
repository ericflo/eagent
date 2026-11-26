package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// closingRecord builds a record event by event, with seqs and times.
type closingRecord struct {
	evs []event.Event
	at  time.Time
}

func (c *closingRecord) add(ev event.Event) int64 {
	ev.Seq = int64(len(c.evs) + 1)
	c.at = c.at.Add(time.Second)
	ev.Time = c.at
	c.evs = append(c.evs, ev)
	return ev.Seq
}

func (c *closingRecord) user(text string) int64 {
	return c.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text}))
}

// call records a reply of eagent's whose call was shown everything up to
// seen.
func (c *closingRecord) call(seen int64) int64 {
	return c.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{SeenSeq: seen}))
}

func (c *closingRecord) said(text string, auto bool) int64 {
	return c.add(event.New(event.Message, event.ActorEagent, event.MessageData{Text: text, Auto: auto}))
}

func (c *closingRecord) last() int64 { return int64(len(c.evs)) }

// owed asks the rule as a resumed session would: from the record alone.
func (c *closingRecord) owed() bool {
	st := state.Replay(c.evs)
	var t turnState
	t.init(st)
	return closingWordOwed(st.LastUserSeq, t.saidSeq, t.spokeSeen)
}

// The rule for the closing word, row by row (closingWordOwed), asked from
// the record as a resumed session asks it.
func TestTheClosingWordIsOwedByWhatTheLastReplySaw(t *testing.T) {
	type when int
	const (
		before when = iota // recorded before the reply's call was made
		during             // recorded while the reply's call was out
		after              // recorded after the reply
	)
	whenName := []string{"before the call", "while the call was out", "after the reply"}
	// build: the prompt, then the user's message and eagent's reply, in
	// the order when says. question: the reply is a question.
	build := func(w when, question bool) *closingRecord {
		c := &closingRecord{at: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)}
		prompt := c.user("merge the pending PRs")
		reply := func(seen int64) {
			c.call(seen)
			if question {
				c.add(event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q1", Text: "Merge #45 as well?"}))
			} else {
				c.said("Codex merged #42 and #44.", false)
			}
		}
		switch w {
		case before:
			m := c.user("and #45?")
			reply(m)
		case during:
			c.user("and #45?")
			reply(prompt) // the call was made before the message
		case after:
			reply(prompt)
			c.user("and #45?")
		}
		return c
	}
	type then struct {
		name string
		do   func(c *closingRecord)
	}
	thens := []then{
		{"nothing", func(*closingRecord) {}},
		{"a later reply that saw it", func(c *closingRecord) { c.call(c.last()); c.said("#45 conflicts; I left it.", false) }},
		{"an automatic notice", func(c *closingRecord) { c.said("I can't reach my own model right now.", true) }},
		{"a stop and resume", func(c *closingRecord) {
			c.add(event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: "interrupted"}))
			c.add(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{}))
		}},
	}
	want := map[when]map[string]bool{
		before: {"nothing": false, "a later reply that saw it": false, "an automatic notice": false, "a stop and resume": false},
		during: {"nothing": true, "a later reply that saw it": false, "an automatic notice": true, "a stop and resume": true},
		after:  {"nothing": true, "a later reply that saw it": false, "an automatic notice": true, "a stop and resume": true},
	}
	for _, question := range []bool{false, true} {
		for _, w := range []when{before, during, after} {
			for _, th := range thens {
				c := build(w, question)
				th.do(c)
				kind := "message"
				if question {
					kind = "question"
				}
				if got := c.owed(); got != want[w][th.name] {
					t.Errorf("user wrote %s (eagent's last word a %s), then %s: owed %v, want %v", whenName[w], kind, th.name, got, want[w][th.name])
				}
			}
		}
	}
	// A later call of the same turn that was shown the message makes the
	// word: it answers it.
	c := &closingRecord{at: time.Now()}
	prompt := c.user("merge the pending PRs")
	c.call(prompt) // a look
	m := c.user("and #45?")
	c.call(m)
	c.said("#45 conflicts; I left it.", false)
	if c.owed() {
		t.Error("a word made by a later call that saw the message is taken for not answering it")
	}
	// A turn cut off after its word: the word stands.
	c.add(event.New(event.TurnEnd, event.ActorEagent, event.TurnData{Reason: "interrupted"}))
	if c.owed() {
		t.Error("a word in a turn that was then cut off is taken for not answering the message it saw")
	}
	// eagent never spoke: owed.
	c = &closingRecord{at: time.Now()}
	c.user("merge the pending PRs")
	if !c.owed() {
		t.Error("a session where eagent never spoke owes no closing word")
	}
	// A record of the eagent of three models has no marks of what its
	// calls saw: record order is all it holds.
	c = &closingRecord{at: time.Now()}
	c.user("merge the pending PRs")
	c.said("Done.", false)
	if c.owed() {
		t.Error("a record with no marks of what was seen: a word after the message is taken for not answering it")
	}
	c.user("and #45?")
	if !c.owed() {
		t.Error("a record with no marks of what was seen: a message after the word is taken as answered")
	}
}

// What the loop holds live is what a resumed session rebuilds from the
// record: a message written while the reply's call was out is owed in both.
func TestTheClosingWordMarksRebuildFromTheRecordAsTheyWereLive(t *testing.T) {
	gate := make(chan struct{})
	called := make(chan struct{}, 1)
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if msgs[len(msgs)-1]["role"] != "tool" && strings.Contains(lastUserText(msgs), "kept for you") && !strings.Contains(allText(msgs), "and #45") {
			called <- struct{}{}
			return reply{calls: say("Codex merged #42 and #44.").calls, block: gate}
		}
		return hold()
	}, nil)
	s.type_("@e how is the merging going?")
	<-called
	s.type_("@e and #45?")
	s.waitFor("the second message", func() bool { return len(s.events(event.UserMessage)) == 2 })
	close(gate)
	s.waitFor("the reply", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	var live, replayed turnState
	var lastUser int64
	s.rt.sync(func() {
		live = s.rt.turns
		lastUser = s.rt.st.LastUserSeq
		replayed.init(state.Replay(append([]event.Event(nil), s.rt.st.Events...)))
	})
	if live.saidSeq != replayed.saidSeq || live.spokeSeen != replayed.spokeSeen {
		t.Fatalf("live: word at %d having seen %d; replayed: at %d having seen %d", live.saidSeq, live.spokeSeen, replayed.saidSeq, replayed.spokeSeen)
	}
	if !closingWordOwed(lastUser, live.saidSeq, live.spokeSeen) {
		t.Fatal("a message written while the reply was out is taken as answered by it")
	}
}
