package harness

import (
	"slices"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Finding 8 of the fourteenth review: a phone or web message that starts
// with one of eagent's commands whose output shows only in eagent's own
// terminal was taken in and answered there, so the sender saw nothing.

// r14iTexts returns the text of each event of type typ recorded from index
// from on. Loop goroutine (or a runtime with no loop running).
func r14iTexts(rt *Runtime, from int, typ string) []string {
	var out []string
	for _, ev := range rt.st.Events[from:] {
		var d struct {
			Text string `json:"text"`
		}
		if ev.Type == typ && ev.Decode(&d) == nil {
			out = append(out, d.Text)
		}
	}
	return out
}

// /kill from the phone or the web while an agent runs is answered by eagent
// where it was sent: the answer is in the conversation, and nothing is
// typed into the agent.
func TestR14KillFromThePhoneOrWebIsAnsweredThere(t *testing.T) {
	for _, from := range []string{"finalechat", "web"} {
		t.Run(from, func(t *testing.T) {
			seat := newFakeSeat("one")
			useFakeRelays(t, seat)
			rt := newLaunchRuntime(t)
			var said, words []string
			rt.sync(func() {
				n := len(rt.st.Events)
				rt.handleInbox(InboxMessage{Type: "message", Text: "/kill endpoint returns 500", From: from, PhoneID: "m1"})
				said = r14iTexts(rt, n, event.Message)
				words = append(r14iTexts(rt, n, event.UserMessage), r14iTexts(rt, n, event.UserAnswer)...)
			})
			rt.sync(func() {})
			if len(said) != 1 || !strings.HasPrefix(said[0], "/kill went with eagent's own tasks and processes") {
				t.Fatalf("told in the conversation: %q, want the one answer to /kill", said)
			}
			if len(words) != 0 || len(seat.sentTexts()) != 0 {
				t.Fatalf("taken as words %q, typed into the agent %q", words, seat.sentTexts())
			}
		})
	}
}

// The same through the phone mirror: the answer to /kill is posted to the
// thread it came from.
func TestR14KillFromThePhoneIsPostedToTheThread(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "hello", true, func(string, []map[string]any) reply { return hold() }, nil)
	waitFor(t, "the agent", func() bool {
		n := 0
		g.rt.sync(func() { n = len(g.rt.st.ActiveRelays()) })
		return n == 1 && fp.postCount() >= 1
	})
	fp.userReply("/kill endpoint returns 500")
	waitFor(t, "the answer on the phone", func() bool {
		return len(fp.postsWhere(func(p map[string]any) bool {
			body, _ := p["body"].(string)
			return strings.HasPrefix(body, "/kill went with eagent's own tasks and processes")
		})) == 1
	})
	if got := g.seat.sentTexts(); slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, "/kill") }) {
		t.Fatalf("typed into the agent: %q", got)
	}
}

// r14iNoAgent is a running session whose agent has been stopped, and its UI.
func r14iNoAgent(t *testing.T) (*Runtime, *fakeUI) {
	t.Helper()
	useFakeRelays(t, newFakeSeat("one"))
	rt := newLaunchRuntime(t)
	if out := relayToolNamed(t, rt, "relay_control", map[string]any{"action": "stop"}); !strings.Contains(out, "Stopped") {
		t.Fatalf("stop: %s", out)
	}
	return rt, rt.ui.(*fakeUI)
}

// With no agent running, a phone or web message that starts with a command
// whose output shows only in eagent's terminal is words for eagent, as the
// same message is with an agent running (relayPassthrough). So is /dismiss
// when no question is open.
func TestR14TerminalOnlyCommandFromThePhoneIsWords(t *testing.T) {
	rt, _ := r14iNoAgent(t)
	var want, got []string
	rt.sync(func() {
		n := len(rt.st.Events)
		for _, cmd := range []string{"/status", "/s", "/schedules", "/help", "/?", "/tasks", "/procs", "/dismiss"} {
			text := cmd + " returns 500 on staging"
			want = append(want, text)
			rt.handleInbox(InboxMessage{Type: "message", Text: text, From: "finalechat", PhoneID: "m-" + cmd})
		}
		got = r14iTexts(rt, n, event.UserMessage)
	})
	if !slices.Equal(got, want) {
		t.Fatalf("user messages on the record: %q, want %q", got, want)
	}
}

// With no agent running and a question open, a web message "/status" is
// the answer; the same line typed in eagent's terminal still prints the
// status and answers nothing, and /kill typed there is answered there.
func TestR14StatusFromTheWebAnswersTheOpenQuestion(t *testing.T) {
	rt, ui := r14iNoAgent(t)
	var answers, said []string
	rt.sync(func() {
		n := len(rt.st.Events)
		rt.append(event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q1", Text: "Which HTTP endpoint is failing?"}))
		rt.onInput("/status")
		rt.onInput("/kill 4")
		answers, said = r14iTexts(rt, n, event.UserAnswer), r14iTexts(rt, n, event.Message)
	})
	if len(answers) != 0 || len(said) != 0 {
		t.Fatalf("typed at the terminal: taken as the answer %q, told in the conversation %q", answers, said)
	}
	if logs := strings.Join(ui.logged(), "\n"); !strings.Contains(logs, "/kill went with eagent's own tasks and processes") {
		t.Fatalf("the terminal's /kill was not answered there: %s", logs)
	}
	name := r8cPost(t, rt, InboxMessage{Type: "message", Text: "/status", From: "web"})
	var got []string
	open := true
	rt.sync(func() {
		rt.pollInbox()
		got, open = r8cWords(rt, event.UserAnswer, "/status"), rt.st.Question != nil
	})
	r8cOne(t, "user.answer", got, name)
	if open {
		t.Fatal("the question is still open")
	}
}
