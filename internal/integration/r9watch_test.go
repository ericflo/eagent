package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/store"
)

// r9Host resumes as the serve does: the message it is resumed with is
// recorded in the session's ledger as taken in, files or not, and the
// session is hosted from then on (it runs).
type r9Host struct {
	fakeResumeHost
	t       *testing.T
	project string
}

func (h *r9Host) Resume(id string, msg PhoneMessage) error {
	r6Record(h.t, h.project, id, r6Read(msg.ID))
	_ = h.fakeResumeHost.Resume(id, msg)
	h.mu.Lock()
	h.hosted[id] = true
	h.mu.Unlock()
	return nil
}

// r9Front stands in front of an r6Thread fixture: it can fail the file
// downloads or the posts with a status, and keeps what was posted.
type r9Front struct {
	srv   *httptest.Server
	files atomic.Int32 // the status downloads fail with; 0 serves them
	posts atomic.Int32 // the status posts fail with; 0 lets them through
	mu    sync.Mutex
	notes []string
}

func newR9Front(t *testing.T, f *r6Thread) *r9Front {
	t.Helper()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	fr := &r9Front{}
	fr.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := fr.files.Load(); s != 0 && strings.HasPrefix(r.URL.Path, "/files/") {
			http.Error(w, "upstream unavailable", int(s))
			return
		}
		if r.Method == http.MethodPost {
			if s := fr.posts.Load(); s != 0 {
				http.Error(w, "refused", int(s))
				return
			}
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			fr.mu.Lock()
			fr.notes = append(fr.notes, fmt.Sprint(body["body"]))
			fr.mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(fr.srv.Close)
	return fr
}

func (fr *r9Front) posted() []string {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return append([]string(nil), fr.notes...)
}

// r9Stopped is a stopped session that keeps the ledger, whose thread has
// the mirror's first post (the cursor) and one message from the app with a
// file, sent after the session stopped.
func r9Stopped(t *testing.T, body string, file map[string]any) (project, sid string, f *r6Thread, fr *r9Front, host *r9Host, w *resumeWatcher) {
	t.Helper()
	project = t.TempDir()
	sid = finishedSession(t, project, time.Now().Add(-time.Hour))
	r6Record(t, project, sid, r6Run(), r6Read("start"))
	f = newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "start", "", stop.Add(-time.Hour), nil)
	f.say(sid, "m1", body, stop.Add(time.Minute), nil)
	f.mu.Lock()
	f.threads[sid][0]["origin"] = "token"
	f.threads[sid][1]["attachments"] = []map[string]any{file}
	f.mu.Unlock()
	fr = newR9Front(t, f)
	host = &r9Host{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, t: t, project: project}
	w = f.watcher(project, host, t)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	return project, sid, f, fr, host, w
}

func r9Shot() map[string]any {
	return map[string]any{"id": "att-1", "kind": "image", "content_type": "image/png", "filename": "shot.png", "size": 3, "url": "/files/att-1"}
}

func r9Taken(t *testing.T, project, sid, id string) bool {
	t.Helper()
	evs, err := store.Read(r6Info(t, project, sid).Path)
	if err != nil {
		t.Fatal(err)
	}
	return ReadPhoneLedger(evs).Taken[id]
}

// Finding 2: a screenshot sent alone whose download fails, while the
// service refuses posts. The session comes back with the message as the
// running mirror takes one, without the file, and the resume carries the
// file refused: the resumed session records it and its mirror tells the
// sender until the note goes out (harness
// TestK1AWatcherResumeTellsEveryRefusedFileOnce). The watcher itself posts
// nothing, so no notice is said for a reply not taken.
func TestAFileThatCouldNotBeFetchedIsNotDroppedUntold(t *testing.T) {
	_, _, _, fr, host, w := r9Stopped(t, "", r9Shot())
	fr.files.Store(http.StatusBadGateway)
	fr.posts.Store(http.StatusForbidden) // not retried by the client, unlike a 502
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || len(host.resumed[0].Attachments) != 0 || !k3Refused(host.resumed[0], "shot.png") {
		t.Fatalf("resumed %#v, want the message with shot.png refused", host.resumed)
	}
	if posts := fr.posted(); len(posts) != 0 {
		t.Fatalf("the watcher posted %q", posts)
	}
}

// Finding 2's first correction made every failed download wait; an
// independent read proved a file that can never be fetched (404, 403)
// then held the session back for good. As the running mirror does, the
// session comes back with the words, the file refused on the resume for
// the resumed session to tell the sender.
func TestWordsAndAFileThatCannotBeFetchedComeBackWithTheWords(t *testing.T) {
	for _, status := range []int32{http.StatusNotFound, http.StatusForbidden, http.StatusBadGateway} {
		t.Run(http.StatusText(int(status)), func(t *testing.T) {
			_, _, _, fr, host, w := r9Stopped(t, "see the screenshot", r9Shot())
			fr.files.Store(status)
			w.sweep(context.Background())
			if host.calls() != 1 || host.resumed[0].Text != "see the screenshot" || len(host.resumed[0].Attachments) != 0 || !k3Refused(host.resumed[0], "shot.png") {
				t.Fatalf("resumed %#v, want once with the words and shot.png refused", host.resumed)
			}
			if posts := fr.posted(); len(posts) != 0 {
				t.Fatalf("the watcher posted %q", posts)
			}
		})
	}
}

// Finding 2: a file too large to take is left out, refused on the resume
// for the resumed session to tell the sender, whether or not the service
// takes posts.
func TestAFileTooLargeIsLeftOutOnlyOnceTheSenderIsTold(t *testing.T) {
	big := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	_, _, _, fr, host, w := r9Stopped(t, "here it is", big)
	fr.posts.Store(http.StatusForbidden)
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].Text != "here it is" || len(host.resumed[0].Attachments) != 0 || !k3Refused(host.resumed[0], "clip.mov") {
		t.Fatalf("resumed %#v, want the words with clip.mov refused", host.resumed)
	}
	if posts := fr.posted(); len(posts) != 0 {
		t.Fatalf("the watcher posted %q", posts)
	}
}

// r9Running is a running session (its lock held) whose thread has the
// mirror's first post; keeps says its run keeps the ledger.
func r9Running(t *testing.T, keeps bool) (project, sid string, sess *store.Session, f *r6Thread, host *r9Host, w *resumeWatcher) {
	t.Helper()
	project = t.TempDir()
	sid = finishedSession(t, project, time.Now().Add(-time.Hour))
	if keeps {
		r6Record(t, project, sid, r6Run(), r6Read("start"))
	}
	f = newR6Thread(t)
	f.say(sid, "start", "", time.Now().Add(-time.Hour), nil)
	f.mu.Lock()
	f.threads[sid][0]["origin"] = "token"
	f.mu.Unlock()
	sess, _, err := store.Open(r6Info(t, project, sid).Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	host = &r9Host{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, t: t, project: project}
	return project, sid, sess, f, host, f.watcher(project, host, t)
}

// r9Says is a message the person writes from the app: it lands in the
// thread and comes on the event stream.
func r9Says(w *resumeWatcher, f *r6Thread, sid, id, body string) {
	at := time.Now()
	f.say(sid, id, body, at, nil)
	w.consider(context.Background(), finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}, finalechat.Message{ID: id, Sender: "user", Origin: "session", Body: body, CreatedAt: at})
}

// r9Ends ends the running session with its last writes, releases its lock,
// and posts its closing note, the thread's last message from then on.
func r9Ends(t *testing.T, sess *store.Session, f *r6Thread, sid string, evs ...event.Event) {
	t.Helper()
	for _, ev := range append(evs, event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: "done"})) {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	f.mu.Lock()
	f.threads[sid] = append(f.threads[sid], map[string]any{"id": "bye", "sender": "system", "origin": "session", "body": "eagent stopped", "created_at": time.Now().Add(time.Second)})
	f.mu.Unlock()
}

// Finding 7's first correction retried the copy of the message the event
// stream brought; an independent read proved a message deleted in the app
// since was then used to resume the session. The thread is read instead.
func TestADeletedReplyToARunningSessionIsNotResumed(t *testing.T) {
	_, sid, sess, f, host, w := r9Running(t, true)
	r9Says(w, f, sid, "m1", "ignore this, sent by mistake")
	f.mu.Lock()
	for _, m := range f.threads[sid] {
		if m["id"] == "m1" {
			m["deleted"] = true
			m["body"] = ""
		}
	}
	f.mu.Unlock()
	r9Ends(t, sess, f, sid)
	w.sweep(context.Background())
	w.sweep(context.Background())
	if host.calls() != 0 {
		t.Fatalf("resumed with a deleted message: %#v", host.resumed)
	}
	if n := len(w.retries.replies); n != 0 {
		t.Fatalf("%d replies still kept after the session stopped", n)
	}
}

// Finding 7: a message that reaches a session as it shuts down, which the
// session does not take in, resumes it once it has stopped, although the
// thread's last message is then the session's closing note.
func TestAReplyToAStoppingSessionResumesItOnceItHasStopped(t *testing.T) {
	_, sid, sess, f, host, w := r9Running(t, true)
	r9Says(w, f, sid, "m1", "one more thing")
	if host.calls() != 0 {
		t.Fatalf("resumed a running session: %#v", host.resumed)
	}
	r9Ends(t, sess, f, sid)
	w.sweep(context.Background())
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || host.resumed[0].Text != "one more thing" {
		t.Fatalf("resumed with %#v", host.resumed)
	}
}

// Finding 7: the same, but the session took the message in before it
// stopped: it is not taken again, and nothing is kept for it after.
func TestAReplyAStoppingSessionTookInIsNotTakenAgain(t *testing.T) {
	_, sid, sess, f, host, w := r9Running(t, true)
	r9Says(w, f, sid, "m1", "one more thing")
	r9Ends(t, sess, f, sid, r6Took("m1", "one more thing"))
	w.sweep(context.Background())
	if host.calls() != 0 {
		t.Fatalf("took the message in twice: %#v", host.resumed)
	}
	if len(w.retries.replies) != 0 {
		t.Fatalf("still kept: %#v", w.retries.replies)
	}
}

// Finding 7: several messages to one running session keep one of them;
// once it has stopped without taking them, it is resumed once, with the
// first (its poll reads on after it), and one quiet entry is kept after,
// for when it stops again.
func TestRepliesToARunningSessionKeepOneEntry(t *testing.T) {
	_, sid, sess, f, host, w := r9Running(t, true)
	for _, id := range []string{"m1", "m2", "m3"} {
		r9Says(w, f, sid, id, "message "+id)
	}
	w.sweep(context.Background()) // while it runs
	if len(w.retries.replies) != 1 || w.retries.replies["m3"].msg.ID != "m3" || w.retries.replies["m3"].told {
		t.Fatalf("kept %#v", w.retries.replies)
	}
	if host.calls() != 0 {
		t.Fatalf("resumed a running session: %#v", host.resumed)
	}
	r9Ends(t, sess, f, sid)
	w.sweep(context.Background())
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" {
		t.Fatalf("resumed with %#v", host.resumed)
	}
	if p := w.retries.replies["m1"]; len(w.retries.replies) != 1 || p.msg.ID != "m1" || p.told {
		t.Fatalf("kept %#v", w.retries.replies)
	}
}

// A reply kept after a failed resume, the thread told, is left as it is
// when a message reaches its session while it runs.
func TestAReplyToARunningSessionLeavesAToldReplyAlone(t *testing.T) {
	_, sid, _, f, _, w := r9Running(t, true)
	told := pendingReply{thread: finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}, msg: finalechat.Message{ID: "m0", Sender: "user"}, since: time.Now(), told: true}
	w.retries.replies["m0"] = told
	r9Says(w, f, sid, "m1", "one more thing")
	w.sweep(context.Background())
	if p := w.retries.replies["m0"]; len(w.retries.replies) != 2 || !p.told || p.msg.ID != "m0" || !p.since.Equal(told.since) || w.retries.replies["m1"].told {
		t.Fatalf("kept %#v", w.retries.replies)
	}
}

// A running session whose run does not keep the ledger (an eagent from
// before it) cannot say afterwards whether it took a message in: nothing is
// kept for it, as before.
func TestARunWithoutTheLedgerKeepsNoReply(t *testing.T) {
	_, sid, sess, f, host, w := r9Running(t, false)
	r9Says(w, f, sid, "m1", "one more thing")
	r9Ends(t, sess, f, sid) // it took m1 in without a record of it
	w.sweep(context.Background())
	if host.calls() != 0 || len(w.retries.replies) != 0 {
		t.Fatalf("resumed %#v, kept %#v", host.resumed, w.retries.replies)
	}
}
