package harness

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// A session that stopped on a question of eagent's, with no agent running,
// is resumed with the answer. The answer is recorded whatever it begins
// with: an absolute path ("/home/u/notes.txt") is not a slash command.
func TestAnAnswerThatIsAPathIsKeptWhenItResumesTheSession(t *testing.T) {
	asked := false
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(allText(msgs), "where are the notes") && !asked && msgs[len(msgs)-1]["role"] != "tool" {
			asked = true
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which file should I read?"}`)}}
		}
		return hold()
	}, nil)
	s.settled()
	// No agent: stop the one the session started.
	s.type_("/stop-relay")
	s.waitFor("the agent to stop", func() bool {
		n := 1
		s.read(func(st *state.State) { n = len(st.ActiveRelays()) })
		return n == 0
	})
	s.settled()
	s.type_("@e where are the notes?")
	s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
	s.settled()
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()

	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := Resume(testConfig(s.model.srv.URL), Options{Project: project, Interactive: true, Prompt: "/home/u/notes.txt"}, ui, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	var answers []string
	for _, ev := range rt.st.Events {
		if ev.Type == event.UserAnswer {
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			answers = append(answers, d.Text)
		}
	}
	if len(answers) != 1 || answers[0] != "/home/u/notes.txt" {
		t.Fatalf("the answer the session was resumed with was not recorded: answers=%q, question still open=%v, ui log=%q", answers, rt.st.Question != nil, ui.logged())
	}
}

// The same answer given while the session runs, typed at the terminal or
// sent from the phone: with a question open and no agent to take a slash
// word, a path is the answer, while eagent's own commands stay commands.
func TestAnAnswerThatIsAPathIsKeptWhileTheSessionRuns(t *testing.T) {
	for _, from := range []string{"", "phone"} {
		t.Run("from="+from, func(t *testing.T) {
			var asked atomic.Bool
			s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
				if strings.Contains(allText(msgs), "where are the notes") && msgs[len(msgs)-1]["role"] != "tool" && asked.CompareAndSwap(false, true) {
					return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which file should I read?"}`)}}
				}
				return hold()
			}, nil)
			s.settled()
			s.type_("/stop-relay")
			s.waitFor("the agent to stop", func() bool {
				n := 1
				s.read(func(st *state.State) { n = len(st.ActiveRelays()) })
				return n == 0
			})
			s.settled()
			s.type_("@e where are the notes?")
			s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
			s.settled()
			give := func(text string) {
				if from == "" {
					s.type_(text)
					return
				}
				s.rt.sync(func() { s.rt.handleInbox(InboxMessage{Type: "message", Text: text, From: from}) })
			}
			answers := func() []string {
				var out []string
				for _, ev := range s.events(event.UserAnswer) {
					var d event.UserAnswerData
					_ = ev.Decode(&d)
					out = append(out, d.Text)
				}
				return out
			}
			// One of eagent's own commands is still a command (/status,
			// which shows only in eagent's terminal, is words from the
			// phone: r14inbox_test).
			give("/kill")
			s.settled()
			if got := answers(); len(got) != 0 {
				t.Fatalf("/kill was taken as the answer: %q", got)
			}
			give("/home/u/notes.txt")
			s.waitFor("the answer on the record", func() bool { return len(answers()) == 1 })
			if got := answers(); got[0] != "/home/u/notes.txt" {
				t.Fatalf("answers = %q", got)
			}
		})
	}
}

// A session that stopped on a question and is resumed with /dismiss closes
// the card an earlier run put on the phone, as dismissed: this run knows
// that card only from the record.
func TestDismissThatResumesASessionClosesTheEarlierRunsCard(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close) // after the session has stopped: Close waits for the long poll
	g := phoneStart(t, fp, "hello", true, func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case strings.Contains(tail, "finished a turn") && strings.Contains(all, "this comes from the phone") && !strings.Contains(all, "Which colour?"):
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which colour?","options":["red","blue"]}`)}}
		}
		return hold()
	}, nil)
	waitFor(t, "the thread to exist", func() bool { return fp.postCount() >= 1 })
	fp.userReply("this comes from the phone")
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	// The card is known to a later run only once it is on the record.
	waitFor(t, "the card on the record", func() bool {
		for _, ev := range g.log() {
			var c event.PhoneQuestionData
			if ev.Type == event.PhoneQuestion && ev.Decode(&c) == nil && c.CardID != "" {
				return true
			}
		}
		return false
	})
	g.interrupt()
	if n := fp.pendingQuestions(); n != 1 {
		t.Fatalf("%d cards open after the stop, want the one", n)
	}
	g.resume(Options{Project: g.project, Interactive: true, Prompt: "/dismiss"})
	waitFor(t, "the earlier run's card to close", func() bool { return fp.pendingQuestions() == 0 })
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if len(fp.dismissedByToken) != 1 || len(fp.cancelled) != 0 {
		t.Fatalf("dismissed %v, withdrawn %v: want the card dismissed", fp.dismissedByToken, fp.cancelled)
	}
}
