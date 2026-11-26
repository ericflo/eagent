package harness

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// r8BeforeMirror stands in front of the phone service and runs before once,
// just before the first post that mirrors a message of the user's.
func r8BeforeMirror(t *testing.T, target string, before func()) string {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var ran atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if bytes.Contains(body, []byte(`"eagent":"mirror"`)) && ran.CompareAndSwap(false, true) {
				before()
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Finding 3 of the eighth review: a session with no phone cursor is resumed
// at the terminal with an answer (--answer), which is recorded before the
// mirror starts and so is mirrored before the poll does. A reply written in
// the app after the check for waiting messages and before that mirror post
// is taken, and so is the one after it, each once and in order.
func TestAReplyWrittenWhileTheTerminalWordsAreMirroredIsTaken(t *testing.T) {
	cfg, project, path, fp := stoppedRelaySession(t)
	bphoneOlder(t, path)
	var first string
	cfg.Finalechat.BaseURL = r8BeforeMirror(t, fp.srv.URL, func() {
		first = fp.userSays("one thing before you go on", nil)
	})
	polls := fp.getCount()
	rt, stop := bphoneRun(t, func() *Runtime {
		useFakeRelays(t, newFakeSeat("Done."))
		rt, err := Resume(cfg, Options{Project: project, Interactive: true, Answer: "carry on with the login test"}, &fakeUI{input: make(chan string, 4)}, path)
		if err != nil {
			t.Fatal(err)
		}
		return rt
	})
	waitFor(t, "the live poll", func() bool { return fp.getCount() > polls+1 })
	second := fp.userSays("and one more", nil)
	waitFor(t, "the later reply in the record", func() bool {
		ids := r6PhoneIDs(path)
		return len(ids) > 0 && ids[len(ids)-1] == second
	})
	rt.sync(func() {})
	stop()
	if got := r6PhoneIDs(path); len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("recorded %v, want %v", got, []string{first, second})
	}
}

// r8Choices is n short distinct choices.
func r8Choices(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("branch-%02d", i+1)
	}
	return out
}

// r8AskSession is a session with no terminal whose model asks question
// with the choices once its agent has finished a turn, and stops the agent.
func r8AskSession(t *testing.T, fp *phoneFake, question string, choices []string) *phoneRig {
	t.Helper()
	g := phoneStart(t, fp, "pick the branch to ship", false, func(_ string, msgs []map[string]any) reply {
		all := allText(msgs)
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			if strings.Contains(all, "Shipping it") {
				return hold()
			}
			return say("Shipping it; done.")
		case strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(all, question):
			return phoneAskAndStop(question, choices...)
		}
		return hold()
	}, nil)
	return g
}

// Finding 7 of the eighth review: the service takes at most twenty choices
// on a card. A question with twenty-one still reaches the phone, as a
// message that carries the question and every choice; the session waits as
// it does on a card, and the user's reply answers it.
func TestAQuestionThePhoneRefusesAsACardReachesItAsAMessage(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	choices := r8Choices(21)
	g := r8AskSession(t, fp, "Which branch?", choices)
	asked := func() []map[string]any {
		return fp.postsWhere(func(p map[string]any) bool {
			body, _ := p["body"].(string)
			return strings.Contains(body, "Which branch?") && p["sender"] != "user"
		})
	}
	waitFor(t, "the question in the thread", func() bool { return len(asked()) == 1 })
	body, _ := asked()[0]["body"].(string)
	for i, c := range choices {
		if !strings.Contains(body, fmt.Sprintf("%d. %s", i+1, c)) {
			t.Fatalf("choice %d is not in the message:\n%s", i+1, body)
		}
	}
	if n := fp.pendingQuestions(); n != 0 {
		t.Fatalf("%d cards, want none: the service refused it", n)
	}
	g.agentStopped()
	g.running(10*loopPoll, "while its question waits on the phone")
	fp.userReply("21")
	if code := g.exit(25 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	var answers []event.UserAnswerData
	for _, ev := range g.log() {
		if ev.Type == event.UserAnswer {
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			answers = append(answers, d)
		}
	}
	if len(answers) != 1 || answers[0].Text != "branch-21" || answers[0].Source != "finalechat" || answers[0].QuestionID != "q1" {
		t.Fatalf("answers = %+v", answers)
	}
	if got := asked(); len(got) != 1 {
		t.Fatalf("the question was posted %d times", len(got))
	}
}

// Twenty choices are a card, as before, and no message stands in for it.
func TestAQuestionWithTwentyChoicesIsACard(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r8AskSession(t, fp, "Which branch?", r8Choices(20))
	waitFor(t, "the card", func() bool { return fp.pendingQuestions() == 1 })
	g.agentStopped()
	g.running(5*loopPoll, "while waiting on the card")
	if got := fp.postsWhere(func(p map[string]any) bool {
		body, _ := p["body"].(string)
		return strings.Contains(body, "Which branch?")
	}); len(got) != 0 {
		t.Fatalf("a message stands in for a card that was posted: %v", got)
	}
}
