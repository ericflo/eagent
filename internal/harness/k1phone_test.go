package harness

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
)

// k1Recreating is the fake service with one twist: just before it takes
// the first post that match picks, the user deletes the thread in the app,
// so that post creates it again under the id t2.
func k1Recreating(t *testing.T, fp *phoneFake, match func(body map[string]any) bool) *httptest.Server {
	svc := newR13Service(t, fp)
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") && !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			if json.Unmarshal(raw, &body) == nil && match(body) {
				once.Do(func() { svc.deleteThread("t2") })
			}
		}
		svc.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// k1ThreadAfter waits for the post match picks, then for the phone's
// worker to finish with it, and fails unless the session now knows the
// thread by the id that post named.
func k1ThreadAfter(t *testing.T, g *phoneRig, match func(body map[string]any) bool) {
	t.Helper()
	waitFor(t, "the post", func() bool { return len(g.fp.postsWhere(match)) > 0 })
	r14Drained(t, g)
	var p *phone
	g.rt.sync(func() { p = g.rt.phone })
	p.mu.Lock()
	id := p.threadID
	p.mu.Unlock()
	if id != "t2" {
		t.Fatalf("the post recreated the thread as t2; the session still knows it as %q", id)
	}
}

func k1Kind(kind string) func(map[string]any) bool {
	return func(b map[string]any) bool {
		meta, _ := b["meta"].(map[string]any)
		return meta["kind"] == kind
	}
}

func k1UserSaid(text string) func(map[string]any) bool {
	return func(b map[string]any) bool { return b["sender"] == "user" && b["body"] == text }
}

// k1TwoOpenCards is a session on a service that keeps no client keys
// whose open question stands on two cards, both still open: the first
// run's, which the service would not let the second run withdraw, and the
// second run's. It is stopped, and the service lets cards be withdrawn
// again. It returns both card ids, the earlier first.
func k1TwoOpenCards(t *testing.T, fp *phoneFake) (*phoneRig, []string) {
	t.Helper()
	fp.features = []string{"activity", "dismiss", "attachments"}
	g := r5AskedAndStopped(t, fp)
	var first event.PhoneQuestionData
	_ = r13Logged(g, event.PhoneQuestion)[0].Decode(&first)
	keep := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/questions/"+first.CardID+"/cancel" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no question"}}`))
			return
		}
		fp.handle(w, r)
	}))
	t.Cleanup(keep.Close)
	normal := g.cfg.Finalechat.BaseURL
	g.cfg.Finalechat.BaseURL = keep.URL
	g.resume(Options{})
	waitFor(t, "the question on a second card", func() bool { return fp.pendingQuestions() == 2 })
	waitFor(t, "the second card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) == 2 })
	r14Drained(t, g)
	g.interrupt()
	g.cfg.Finalechat.BaseURL = normal
	var second event.PhoneQuestionData
	_ = r13Logged(g, event.PhoneQuestion)[1].Decode(&second)
	if s1, s2 := r14Status(fp, first.CardID), r14Status(fp, second.CardID); s1 != "pending" || s2 != "pending" {
		t.Fatalf("the cards are %q and %q, want both open", s1, s2)
	}
	return g, []string{first.CardID, second.CardID}
}

// B15: the question is answered by the words the session is resumed
// with. Every card of it that may still be open is withdrawn, not only the
// newest: a tap on the other would give the answer a second time.
func TestK1AResumeThatAnswersWithdrawsEveryOpenCard(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"words", Options{Interactive: true, Prompt: "blue"}},
		{"an answer", Options{Interactive: true, Answer: "blue"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newPhoneFake(true)
			t.Cleanup(fp.srv.Close)
			g, cards := k1TwoOpenCards(t, fp)
			g.resume(tc.opts)
			waitFor(t, "the answer", func() bool { return len(r13Logged(g, event.UserAnswer)) == 1 })
			waitFor(t, "the thread to be attached", func() bool { return len(g.events(event.PhoneThread)) > 0 })
			r14Drained(t, g)
			for _, fid := range cards {
				if s := r14Status(fp, fid); s == "pending" {
					t.Errorf("card %s of the answered question is still open (cards %v)", fid, cards)
				}
			}
		})
	}
}

// k1AskedAsMessage is how many times the question went to the thread as a
// message.
func k1AskedAsMessage(fp *phoneFake, question string) int {
	return len(fp.postsWhere(func(p map[string]any) bool {
		body, _ := p["body"].(string)
		return strings.Contains(body, question) && p["sender"] != "user"
	}))
}

// B5: on a service that keeps no client keys, a question the service
// refused as a card went to the thread as a message, and the session
// stopped with it open. A message does not expire or close as a card
// does, so the resume does not post it a second time; the session still
// waits for the reply, which answers it.
func TestK1AQuestionPostedAsAMessageIsNotPostedAgainOnResume(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	fp.features = []string{"activity", "dismiss", "attachments"}
	g := r8AskSession(t, fp, "Which branch?", r8Choices(21))
	waitFor(t, "the question in the thread", func() bool { return k1AskedAsMessage(fp, "Which branch?") == 1 })
	g.agentStopped()
	r14Drained(t, g)
	g.rt.sync(func() {})
	g.interrupt()
	fp.mu.Lock()
	asks := len(fp.asks)
	fp.mu.Unlock()
	g.resume(Options{})
	waitFor(t, "the question to be asked again", func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return len(fp.asks) > asks })
	r14Drained(t, g)
	if n := k1AskedAsMessage(fp, "Which branch?"); n != 1 {
		t.Fatalf("the question was posted %d times", n)
	}
	g.running(5*loopPoll, "while its question waits on the phone")
	fp.userReply("21")
	if code := g.exit(25 * time.Second); code != 0 {
		t.Fatalf("exit %d logs=%v", code, g.ui.logged())
	}
	var answers []string
	for _, ev := range r13Logged(g, event.UserAnswer) {
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		answers = append(answers, d.Text)
	}
	if len(answers) != 1 || answers[0] != "branch-21" {
		t.Fatalf("answers = %q", answers)
	}
}

// B5, the thread deleted meanwhile: the question's message went with it,
// and the resume posts it in the thread its first post recreated.
func TestK1AQuestionPostedAsAMessageIsPostedAgainInARecreatedThread(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	fp.features = []string{"activity", "dismiss", "attachments"}
	svc := newR13Service(t, fp)
	choices := r8Choices(21)
	g := phoneStart(t, fp, "pick the branch to ship", false, func(_ string, msgs []map[string]any) reply {
		if !phoneLastIsTool(msgs) && strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(allText(msgs), "Which branch?") {
			return phoneAskAndStop("Which branch?", choices...)
		}
		return hold()
	}, func(c *config.Config) { c.Finalechat.BaseURL = svc.srv.URL })
	waitFor(t, "the question in the thread", func() bool { return k1AskedAsMessage(fp, "Which branch?") == 1 })
	g.agentStopped()
	r14Drained(t, g)
	g.rt.sync(func() {})
	g.interrupt()
	svc.deleteThread("t2")
	g.resume(Options{})
	waitFor(t, "the question in the recreated thread", func() bool { return k1AskedAsMessage(fp, "Which branch?") == 2 })
}

// B2: a session last run without the ledger is resumed at the terminal
// while two app messages wait, and the read that finds them fails for
// good (the service answers 400) the first time. A message the person
// writes after the resume is not taken past them: they are taken first,
// in order, once the thread can be read.
func TestK1AReadThatFailsForGoodAtResumeTakesNothingPastWhatWaited(t *testing.T) {
	setFor(t, &phoneRetryStep, 10*time.Millisecond)
	cfg, project, path, fp := stoppedRelaySession(t)
	bphoneOlder(t, path)
	first := fp.userSays("the checklist is A, B, C", nil)
	second := fp.userSays("are you still there?", nil)
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var failed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") && failed.CompareAndSwap(false, true) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"not now"}}`))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg.Finalechat.BaseURL = srv.URL
	rt, stop := bphoneRun(t, func() *Runtime {
		useFakeRelays(t, newFakeSeat("Done."))
		rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
		if err != nil {
			t.Fatal(err)
		}
		return rt
	})
	waitFor(t, "the resumed session's first post", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		for _, p := range fp.posts {
			if p["body"] == "eagent session resumed here." {
				return true
			}
		}
		return false
	})
	later := fp.userSays("run the tests", nil)
	waitFor(t, "the later message in the record", func() bool { return slices.Contains(r6PhoneIDs(path), later) })
	rt.sync(func() {})
	stop()
	if got := r6PhoneIDs(path); !slices.Equal(got, []string{first, second, later}) {
		t.Fatalf("recorded %v, want %v", got, []string{first, second, later})
	}
}

// B16: every post of eagent's that the service answers with a thread
// takes that thread's id, so a thread the user deleted and the post
// recreated is known by its new id (as a narrator message or a question
// already is).
func TestK1EveryPostTakesTheThreadItNames(t *testing.T) {
	held := func(string, []map[string]any) reply { return hold() }
	t.Run("words typed at the terminal", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1UserSaid("hello from the terminal")
		srv := k1Recreating(t, fp, match)
		g := phoneStart(t, fp, "start work", true, held, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
		waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
		g.ui.input <- "hello from the terminal"
		k1ThreadAfter(t, g, match)
	})
	t.Run("an answer given at the terminal", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1Kind("answer")
		srv := k1Recreating(t, fp, match)
		g := phoneStart(t, fp, "paint it", true, r14AskBrain(), func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
		waitFor(t, "the card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) == 1 })
		g.ui.input <- "blue"
		k1ThreadAfter(t, g, match)
	})
	t.Run("what eagent typed to the agent", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1Kind("relay_send")
		srv := k1Recreating(t, fp, match)
		g := phoneStart(t, fp, "paint it", true, func(_ string, msgs []map[string]any) reply {
			if !phoneLastIsTool(msgs) && strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(allText(msgs), "paint the second coat") {
				return reply{calls: []event.ToolCall{tc("relay_send", `{"text":"paint the second coat"}`)}}
			}
			return hold()
		}, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
		k1ThreadAfter(t, g, match)
	})
	t.Run("a file refused", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1Kind("attachment_refused")
		srv := k1Recreating(t, fp, match)
		g := phoneStart(t, fp, "start work", true, held, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
		waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
		big := map[string]any{"id": "big-9", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/api/v1/attachments/big-9"}
		r10Says(fp, r10Msg("and this", big))
		k1ThreadAfter(t, g, match)
	})
	t.Run("the words a session was resumed with", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		match := k1UserSaid("blue, and carry on")
		srv := k1Recreating(t, fp, match)
		g := r5AskedAndStopped(t, fp) // its agent stopped
		g.cfg.Finalechat.BaseURL = srv.URL
		g.resume(Options{Interactive: true, Prompt: "blue, and carry on"})
		k1ThreadAfter(t, g, match)
	})
}

// B13, on a service that keeps client keys: the resumed session asks its
// open question again, and the service answers with the earlier card
// itself. While that ask is in flight the person closes the card and
// writes; their words are not the answer to the question they dismissed.
func TestK1DismissedCardThenWordsWhileTheKeyedAskIsInFlight(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	g := r5AskedAndStopped(t, fp)
	var first event.PhoneQuestionData
	_ = r13Logged(g, event.PhoneQuestion)[0].Decode(&first)
	var once sync.Once
	var words string
	fp.mu.Lock()
	fp.onAsk = func() {
		once.Do(func() {
			fp.mu.Lock()
			q := fp.questions[first.CardID]
			q["status"], q["answered_at"] = "dismissed", time.Now().UTC().Format(time.RFC3339Nano)
			fp.cond.Broadcast()
			fp.mu.Unlock()
			words = r10Says(fp, r10Msg("never mind, check the tests first"))[0]
			r14Until(func() bool {
				for _, ev := range r13Logged(g, event.UserAnswer) {
					var d event.UserAnswerData
					if ev.Decode(&d) == nil && d.PhoneID == words {
						return true
					}
				}
				return r14Taken(g, words) > 0
			})
		})
	}
	fp.mu.Unlock()
	g.resume(Options{Interactive: true})
	waitFor(t, "the card to be recorded again", func() bool { return len(r13Logged(g, event.PhoneQuestion)) == 2 })
	r14Drained(t, g)
	g.rt.sync(func() {})
	for _, ev := range r13Logged(g, event.UserAnswer) {
		var d event.UserAnswerData
		if ev.Decode(&d) == nil && d.PhoneID == words {
			t.Fatalf("the person dismissed the card and then wrote %q; it was taken as the answer (dismissals %d)", d.Text, len(r13Logged(g, event.UserDismiss)))
		}
	}
}
