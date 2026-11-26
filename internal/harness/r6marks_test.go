package harness

import (
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// closingMarks compares the closing-word marks the live loop holds with the
// ones a resumed session rebuilds from the same record.
func closingMarks(t *testing.T, s *session) (owedLive, owedReplayed bool) {
	t.Helper()
	var live, replayed turnState
	var lastUser int64
	s.rt.sync(func() {
		live = s.rt.turns
		lastUser = s.rt.st.LastUserSeq
		replayed.init(state.Replay(append([]event.Event(nil), s.rt.st.Events...)))
	})
	if live.saidSeq != replayed.saidSeq || live.spokeSeen != replayed.spokeSeen {
		t.Errorf("live: word at %d having seen %d; replayed: at %d having seen %d", live.saidSeq, live.spokeSeen, replayed.saidSeq, replayed.spokeSeen)
	}
	return closingWordOwed(lastUser, live.saidSeq, live.spokeSeen), closingWordOwed(lastUser, replayed.saidSeq, replayed.spokeSeen)
}

// Each way a word can be made, with the user writing while the call that
// makes it (or the one before it) is out: the marks are the same live and
// replayed, and the closing word is owed exactly when the word's call was
// not shown the message.
func TestTheClosingMarksAreTheSameLiveAndAfterAResume(t *testing.T) {
	cases := []struct {
		name string
		// first is the gated reply to the first message; then answers
		// every later call that is not after a tool result.
		first func() reply
		then  func(msgs []map[string]any) reply
		owed  bool
	}{
		{"a later call of the turn makes the word", func() reply {
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"merge"}`)}}
		}, func(msgs []map[string]any) reply {
			if strings.Contains(allText(msgs), "and #45?") {
				return say("#45 conflicts; I left it.")
			}
			return hold()
		}, false},
		{"a reply sent back for no tool call, then the word", func() reply {
			return reply{text: "Codex merged #42 and #44."}
		}, func(msgs []map[string]any) reply {
			if strings.Contains(allText(msgs), "and #45?") {
				return hold()
			}
			return say("Codex merged #42 and #44.")
		}, true},
		{"a question", func() reply {
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge #45 as well?"}`)}}
		}, func(msgs []map[string]any) reply { return hold() }, true},
		{"the word in the first call", func() reply { return say("Codex merged #42 and #44.") },
			func(msgs []map[string]any) reply { return hold() }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gate := make(chan struct{})
			called := make(chan struct{}, 1)
			var mu sync.Mutex
			first := true
			s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
				mu.Lock()
				defer mu.Unlock()
				last := msgs[len(msgs)-1]
				if first && last["role"] != "tool" && strings.Contains(lastUserText(msgs), "kept for you") {
					first = false
					called <- struct{}{}
					r := c.first()
					r.block = gate
					return r
				}
				return c.then(msgs)
			}, nil)
			s.type_("@e how is the merging going?")
			<-called
			s.type_("@e and #45?")
			s.waitFor("the second message", func() bool { return len(s.events(event.UserMessage)) == 2 })
			close(gate)
			s.waitFor("a word", func() bool { return len(s.events(event.Message, event.Question)) > 0 })
			s.settled()
			live, replayed := closingMarks(t, s)
			if live != c.owed || replayed != c.owed {
				t.Errorf("closing word owed live %v, replayed %v; want %v; said %q", live, replayed, c.owed, s.ui.said())
			}
		})
	}
}
