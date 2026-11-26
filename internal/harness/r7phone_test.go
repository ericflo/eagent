package harness

import (
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

// r7FailFirstRead stands in front of the phone service and fails its first
// read of a thread's messages as a service briefly down does.
func r7FailFirstRead(t *testing.T, target string) string {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var failed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") && failed.CompareAndSwap(false, true) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"down for a moment"}}`))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Finding 15: a session last run without the ledger, resumed at the
// terminal while two app messages wait, takes both when the first read of
// the thread fails once.
func TestMessagesThatWaitedAreTakenWhenTheFirstReadFails(t *testing.T) {
	setFor(t, &phoneRetryStep, 10*time.Millisecond)
	cfg, project, path, fp := stoppedRelaySession(t)
	bphoneOlder(t, path)
	first := fp.userSays("the checklist is A, B, C", nil)
	second := fp.userSays("are you still there?", nil)
	cfg.Finalechat.BaseURL = r7FailFirstRead(t, fp.srv.URL)
	rt, stop := bphoneRun(t, func() *Runtime {
		useFakeRelays(t, newFakeSeat("Done."))
		rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
		if err != nil {
			t.Fatal(err)
		}
		return rt
	})
	waitFor(t, "both in the record", func() bool { return len(r6PhoneIDs(path)) >= 2 })
	rt.sync(func() {})
	stop()
	if got := r6PhoneIDs(path); len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("recorded %v, want %v", got, []string{first, second})
	}
}

// Finding 17: words written on the open question's card while the session
// was stopped are its answer, verbatim, as the running poll takes them: a
// leading slash runs no command, and a number is not turned into an option.
func TestACardAnswerThatResumesIsTakenVerbatim(t *testing.T) {
	for _, words := range []string{"/status", "1"} {
		t.Run(words, func(t *testing.T) {
			fp := newPhoneFake(true)
			t.Cleanup(fp.srv.Close)
			g := r5AskedAndStopped(t, fp)
			card := r5CardOf(t, g)
			if !fp.answer(words, "") {
				t.Fatal("no card to answer")
			}
			g.resume(Options{Prompt: words, PromptSource: "finalechat", PromptAnswers: card})
			waitFor(t, "the answer to be recorded", func() bool { n, open := r5Answers(g); return n == 1 && !open })
			for _, ev := range g.log() {
				var d event.UserAnswerData
				if ev.Type == event.UserAnswer && ev.Decode(&d) == nil && d.Text != words {
					t.Fatalf("the card's answer %q was recorded as %q", words, d.Text)
				}
			}
		})
	}
}
