package harness

import (
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
	"github.com/ericflo/eagent/internal/state"
)

// c328c06: the read that finds what waited (after_time / before) fails for
// good every time (a service that refuses it). At f36e19c the poll still
// read on from the session's own post and took what the person wrote after
// the resume; now it takes nothing for the whole run, and says nothing.
func TestRV15ReadOfWhatWaitedAlwaysRefusedTakesNothingEver(t *testing.T) {
	setFor(t, &phoneRetryStep, 10*time.Millisecond)
	cfg, project, path, fp := stoppedRelaySession(t)
	bphoneOlder(t, path)
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") && (q.Has("after_time") || q.Has("before")) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"no"}}`))
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
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && !slices.Contains(r6PhoneIDs(path), later) {
		time.Sleep(50 * time.Millisecond)
	}
	rt.sync(func() {})
	stop()
	if !slices.Contains(r6PhoneIDs(path), later) {
		t.Fatalf("a message written 8s after the resume was never taken: recorded %v", r6PhoneIDs(path))
	}
}

// 4a93e3b: the record of a question posted as a message (empty card_id,
// thread set) makes liveCards name a card "" (its second loop does not
// skip an empty card_id), and a resume that cannot ask the service then
// counts that "card" as unknown: words from the phone become a plain
// message instead of the answer, as they were at f36e19c (no record, no
// card, rule 4).
func TestRV15QuestionPostedAsMessageIsNotACardAtResume(t *testing.T) {
	st := &state.State{Question: &state.Question{ID: "q1", Text: "Which?"}}
	st.Events = append(st.Events, event.New(event.PhoneQuestion, event.ActorHarness, event.PhoneQuestionData{QuestionID: "q1", ThreadID: "th-1"}))
	if got := liveCards(st, "q1"); len(got) != 0 {
		t.Errorf("liveCards = %q, want none (the question went as a message, there is no card)", got)
	}
	off := false
	r := &Runtime{st: st}
	r.cfg.Finalechat.Enabled = &off
	plan := decideResume(r.readCards(""), resumeWords{FromPhone: true})
	if plan.Words != wordsAnswer {
		t.Fatalf("phone words at resume became %v (message), want the answer as with no card: cards %+v", plan.Words, r.readCards(""))
	}
}

// k1CardCalls stands in front of the fake service on a service without
// client keys: it refuses every question as a card while refuse is set,
// and keeps the path of every call made about one card.
func k1CardCalls(t *testing.T, fp *phoneFake) (url string, refuse *atomic.Bool, calls func() []string) {
	t.Helper()
	fp.features = []string{"activity", "dismiss", "attachments"}
	refuse = &atomic.Bool{}
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/questions/") {
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
		}
		if refuse.Load() && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/questions") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"code":"validation_failed","message":"no card"}}`))
			return
		}
		fp.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, refuse, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

// k1Blank reports the calls that name a card with an empty id.
func k1Blank(paths []string) []string {
	var out []string
	for _, p := range paths {
		if strings.HasSuffix(p, "/questions/") || strings.Contains(p, "/questions//") {
			out = append(out, p)
		}
	}
	return out
}

// The same, in the running session and at a resume: a question that went
// to the thread as a message has no card, so no call is ever made about a
// card with an empty id, and a real card of the question recorded before
// it is still the one withdrawn when the resume answers.
func TestK1AQuestionPostedAsAMessageIsNeverACard(t *testing.T) {
	asks := func(t *testing.T, fp *phoneFake, u string) *phoneRig {
		choices := r8Choices(3)
		return phoneStart(t, fp, "pick the branch to ship", false, func(_ string, msgs []map[string]any) reply {
			if !phoneLastIsTool(msgs) && strings.Contains(lastUserText(msgs), "finished a turn") && !strings.Contains(allText(msgs), "Which branch?") {
				return phoneAskAndStop("Which branch?", choices...)
			}
			return hold()
		}, func(c *config.Config) { c.Finalechat.BaseURL = u })
	}
	settled := func(t *testing.T, g *phoneRig) {
		t.Helper()
		g.agentStopped()
		r14Drained(t, g)
		g.rt.sync(func() {})
	}
	t.Run("a plain message while it is open", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		u, refuse, calls := k1CardCalls(t, fp)
		refuse.Store(true)
		g := asks(t, fp, u)
		waitFor(t, "the question in the thread", func() bool { return k1AskedAsMessage(fp, "Which branch?") == 1 })
		settled(t, g)
		g.interrupt()
		g.resume(Options{Interactive: true})
		waitFor(t, "the phone poll", func() bool { return len(g.events(event.PhoneThread)) == 2 })
		fp.userReply("2")
		waitFor(t, "the reply to answer it", func() bool { return len(r14Answers(g)) == 1 })
		if got := k1Blank(calls()); len(got) > 0 {
			t.Fatalf("calls about a card with no id: %q", got)
		}
	})
	t.Run("a resume that answers it", func(t *testing.T) {
		fp := newPhoneFake(true)
		t.Cleanup(fp.srv.Close)
		u, refuse, calls := k1CardCalls(t, fp)
		g := asks(t, fp, u)
		waitFor(t, "the card to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) == 1 })
		var card event.PhoneQuestionData
		_ = g.events(event.PhoneQuestion)[0].Decode(&card)
		settled(t, g)
		g.interrupt()
		// The service now refuses the card: the next run sends the question
		// as a message, and records that after the card.
		refuse.Store(true)
		g.resume(Options{})
		waitFor(t, "the question in the thread", func() bool { return k1AskedAsMessage(fp, "Which branch?") == 1 })
		waitFor(t, "the message to be recorded", func() bool { return len(g.events(event.PhoneQuestion)) == 2 })
		g.interrupt()
		g.resume(Options{Interactive: true, Answer: "branch-02"})
		waitFor(t, "the thread to be attached", func() bool { return len(g.events(event.PhoneThread)) == 3 })
		r14Drained(t, g)
		if got := k1Blank(calls()); len(got) > 0 {
			t.Fatalf("calls about a card with no id: %q", got)
		}
		if s := r14Status(fp, card.CardID); s != "cancelled" {
			t.Fatalf("the question's card is %q after the resume answered it, want it withdrawn", s)
		}
	})
}

// The reader's suspicion about 4a93e3b: a resume that does not post the
// question again as a message (an earlier run did, in this thread) posts
// nothing to carry the waiting line, and does not mark it shown; the
// status keeper shows it.
func TestK1AQuestionNotPostedAgainStillShowsTheWaitingLine(t *testing.T) {
	quickKeeper(t)
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
	fp.live = nil // the line has run out while the session was stopped
	fp.mu.Unlock()
	g.resume(Options{})
	waitFor(t, "the question to be asked again", func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return len(fp.asks) == 2 })
	r14Drained(t, g)
	g.statusIdle()
	if n := k1AskedAsMessage(fp, "Which branch?"); n != 1 {
		t.Fatalf("the question was posted %d times", n)
	}
	if got := fp.shownStatus(); got != "Waiting for your answer" {
		t.Fatalf("the thread shows %q while the session waits for the answer", got)
	}
}
