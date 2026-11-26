package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
)

// r6Flaky puts a switch in front of the r6Thread fixture: while it is on,
// reading a thread's messages fails as a service under a deploy does.
func r6Flaky(t *testing.T, f *r6Thread) (string, *atomic.Bool) {
	t.Helper()
	target, _ := url.Parse(f.srv.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var failing atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") {
			http.Error(w, `{"error":{"code":"unavailable"}}`, http.StatusBadGateway)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &failing
}

// r6Ledgered is a stopped session whose record keeps the ledger, with its
// own first post in the thread as the cursor.
func r6Ledgered(t *testing.T, f *r6Thread, project string, evs ...event.Event) string {
	t.Helper()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	r6Record(t, project, sid, append([]event.Event{r6Run(), r6Read("start")}, evs...)...)
	f.say(sid, "start", "", r6Info(t, project, sid).Modified.Add(-time.Hour), nil)
	f.mu.Lock()
	f.threads[sid][0]["origin"] = "token"
	f.mu.Unlock()
	return sid
}

// Two messages wait in the thread of a stopped session that keeps the
// ledger; the second comes up on the event stream while the thread cannot
// be read. The session is not resumed with the second ahead of the first:
// the trigger waits for a sweep that can read the thread, which resumes
// it with the first.
func TestAStreamTriggerWhileTheThreadCannotBeReadDoesNotSkipAnEarlierMessage(t *testing.T) {
	project := t.TempDir()
	f := newR6Thread(t)
	sid := r6Ledgered(t, f, project)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "m1", "the checklist is A, B, C", stop.Add(time.Minute), nil)
	f.say(sid, "m2", "are you still there?", stop.Add(2*time.Minute), nil)
	base, failing := r6Flaky(t, f)
	host := &fakeResumeHost{hosted: map[string]bool{}}
	w := f.watcher(project, host, t)
	w.client = &finalechat.Client{BaseURL: base, Token: "fc_fixture"}
	failing.Store(true)
	w.consider(context.Background(), finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid},
		finalechat.Message{ID: "m2", Sender: "user", Origin: "session", Body: "are you still there?"})
	if host.calls() != 0 {
		t.Fatalf("resumed while the thread could not be read: %#v", host.resumed)
	}
	failing.Store(false)
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" {
		t.Fatalf("resumes: %#v", host.resumed)
	}
}

// A record without the ledger keeps what f2b210d did when the thread
// cannot be read: the stream's message resumes the session.
func TestAStreamTriggerOnARecordWithoutTheLedgerStillResumesWhenTheThreadCannotBeRead(t *testing.T) {
	project := t.TempDir()
	f := newR6Thread(t)
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f.say(sid, "m1", "go on", r6Info(t, project, sid).Modified.Add(time.Minute), nil)
	base, failing := r6Flaky(t, f)
	host := &fakeResumeHost{hosted: map[string]bool{}}
	w := f.watcher(project, host, t)
	w.client = &finalechat.Client{BaseURL: base, Token: "fc_fixture"}
	failing.Store(true)
	w.consider(context.Background(), finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid},
		finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "go on"})
	if host.calls() != 1 || host.resumed[0].ID != "m1" {
		t.Fatalf("resumes: %#v", host.resumed)
	}
}

// The service's listing lags its stream: the thread lists nothing after
// the cursor yet, and the stream's message is the next one. It is taken.
func TestAStreamTriggerAheadOfTheListingIsTaken(t *testing.T) {
	project := t.TempDir()
	f := newR6Thread(t)
	sid := r6Ledgered(t, f, project)
	host := &fakeResumeHost{hosted: map[string]bool{}}
	w := f.watcher(project, host, t)
	w.consider(context.Background(), finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid},
		finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "go on"})
	if host.calls() != 1 || host.resumed[0].ID != "m1" {
		t.Fatalf("resumes: %#v", host.resumed)
	}
}

// An answer recorded just before a hard stop names its phone message and
// its card on itself, in that one write: no later sweep or message takes
// it again, whether the thread's newest message is the answer or a later
// one.
//
// Each half of the answer's naming is enough on its own: its phone_id
// names the message, its card_id names the card the message was written
// on. Without either, the message is the next one, which is what the
// "neither" case shows the check can see. Asked of nextPhoneMessage
// directly (the reply watcher's choice, with no time filter), and through
// the sweep with a later message waiting.
func TestAnAnswerRecordedJustBeforeAHardStopIsNotTakenAgain(t *testing.T) {
	for _, c := range []struct {
		name            string
		phoneID, cardID string
		taken           bool
	}{
		{"both", "a1", "card-1", true},
		{"phone_id only", "a1", "", true},
		{"card_id only", "", "card-1", true},
		{"neither", "", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			project := t.TempDir()
			f := newR6Thread(t)
			sid := r6Ledgered(t, f, project,
				event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q1", Text: "which colour?"}),
				event.New(event.PhoneQuestion, event.ActorHarness, event.PhoneQuestionData{QuestionID: "q1", CardID: "card-1"}),
				event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: "q1", Text: "blue", Source: "finalechat", PhoneID: c.phoneID, CardID: c.cardID}),
			)
			stop := r6Info(t, project, sid).Modified
			f.say(sid, "a1", "blue", stop.Add(-time.Second), map[string]any{"kind": "answer", "question_id": "card-1"})
			w := f.watcher(project, &fakeResumeHost{hosted: map[string]bool{}}, t)
			m, found, err := nextPhoneMessage(context.Background(), w.client, r6Info(t, project, sid), nil)
			if err != nil || found == c.taken || (found && m.ID != "a1") {
				t.Fatalf("next message %q found=%v err=%v; want found=%v", m.ID, found, err, !c.taken)
			}
			f.say(sid, "m2", "thanks", stop.Add(time.Minute), nil)
			host := &fakeResumeHost{hosted: map[string]bool{}}
			f.watcher(project, host, t).sweep(context.Background())
			want := "m2"
			if !c.taken {
				want = "a1"
			}
			if host.calls() != 1 || host.resumed[0].ID != want {
				t.Fatalf("resumes %#v, want one with %q", host.resumed, want)
			}
		})
	}
}
