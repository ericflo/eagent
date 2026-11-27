package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
)

// k1Host is a host that hands over every message it resumes with.
type k1Host struct {
	ResumeHost
	next chan PhoneMessage
}

func (h *k1Host) Resume(id string, msg PhoneMessage) error {
	err := h.ResumeHost.Resume(id, msg)
	h.next <- msg
	return err
}

// k1Stream stands in front of an r6Thread fixture with an event stream
// whose first connection brings one message and then holds; the ones
// after it bring nothing.
func k1Stream(t *testing.T, f *r6Thread, thread finalechat.Thread, m map[string]any) string {
	t.Helper()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var sent atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/events" {
			proxy.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sseEvent(w, "ready", map[string]any{})
		if !sent.Swap(true) {
			sseEvent(w, "message.created", map[string]any{"thread": thread, "message": m})
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// k1Next waits for the next resume, for at most two seconds.
func k1Next(t *testing.T, h *k1Host) (PhoneMessage, bool) {
	t.Helper()
	select {
	case m := <-h.next:
		return m, true
	case <-time.After(2 * time.Second):
		return PhoneMessage{}, false
	}
}

// B14: a serve restart brings back a session (PendingReply) whose earliest
// message waiting is /quit, with a message after it. The session resumed
// with the /quit stops at once, its closing note the thread's latest
// message. The reply watcher, started after the restore as the serve starts
// it, resumes the session with the message after the /quit.
func TestK1ARestoreWithAQuitFirstLeavesNothingBehind(t *testing.T) {
	sid, f, quit, _ := r13QuitWaiting(t, "/quit", "actually fix the login bug")
	host := &k1Host{ResumeHost: quit, next: make(chan PhoneMessage, 4)}
	t.Setenv("FINALECHAT_URL", f.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	msg, ok, err := PendingReply(context.Background(), quit.project, r6Info(t, quit.project, sid))
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("pending reply %#v ok=%v err=%v, want the /quit", msg, ok, err)
	}
	if err := host.Resume(sid, msg); err != nil {
		t.Fatal(err)
	}
	<-host.next
	ctx, cancel := context.WithCancel(context.Background())
	stop := StartResumeWatcher(ctx, quit.project, host, t.Logf)
	defer stop()
	defer cancel()
	m, ok := k1Next(t, host)
	if !ok || m.ID != "m2" || m.Text != "actually fix the login bug" {
		t.Fatalf("after the restore's /quit: resumed %v, want the message after it", r13Resumed(quit))
	}
}

// B8: a reply whose resume failed (the thread was told) is taken back in
// the app before the next sweep. The thread lists it as deleted; the copy
// kept at the failed try does not know. The next sweep resumes nothing and
// forgets the reply.
func TestK1AReplyDeletedAfterItsResumeFailedIsNotResumed(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	r6Record(t, project, sid, r6Run(), r6Read("start"))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "start", "", stop.Add(-time.Hour), nil)
	f.say(sid, "m1", "please deploy the fix", stop.Add(time.Minute), nil)
	host := &flakyResumeHost{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, failures: 1}
	w := f.watcher(project, host, t)
	w.sweep(context.Background())
	if p := w.retries.replies["m1"]; host.calls() != 0 || !p.told {
		t.Fatalf("after the failed resume: resumed %#v, kept %#v", host.resumed, w.retries.replies)
	}
	f.mu.Lock()
	f.threads[sid][1]["deleted"] = true
	f.mu.Unlock()
	w.sweep(context.Background())
	if host.calls() != 0 || len(w.retries.replies) != 0 {
		t.Fatalf("after the reply was deleted: resumed %#v, kept %#v", host.resumed, w.retries.replies)
	}
}

// B9: a reply reaches a session while it runs, and the reply watcher keeps
// it for when the session stops. The serve restarts (a new process knows
// nothing the last one held in memory), and then the session stops
// without having taken the reply in, its closing note the thread's latest
// message. The new serve's watcher resumes the session with the reply.
func TestK1AReplyKeptWhileItsSessionRanOutlivesARestartOfTheServe(t *testing.T) {
	project, sid, sess, f, running, _ := r9Running(t, true)
	host := &k1Host{ResumeHost: running, next: make(chan PhoneMessage, 4)}
	at := time.Now()
	f.say(sid, "m1", "one more thing", at, nil)
	thread := finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}
	t.Setenv("FINALECHAT_URL", k1Stream(t, f, thread, map[string]any{"id": "m1", "sender": "user", "origin": "session", "body": "one more thing", "created_at": at}))
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	left := make(chan struct{}, 1)
	logf := func(format string, args ...any) {
		t.Logf(format, args...)
		if strings.Contains(format, "left to its running mirror") {
			select {
			case left <- struct{}{}:
			default:
			}
		}
	}
	stop := StartResumeWatcher(context.Background(), project, host, logf)
	select {
	case <-left:
	case <-time.After(2 * time.Second):
		stop()
		t.Fatal("the reply never reached the first serve's watcher")
	}
	stop()
	watchRetries.Delete(project) // the next serve is another process
	r9Ends(t, sess, f, sid)
	stop = StartResumeWatcher(context.Background(), project, host, t.Logf)
	defer stop()
	if m, ok := k1Next(t, host); !ok || m.ID != "m1" || m.Text != "one more thing" {
		t.Fatalf("after the restart: resumed %#v (%v), want the reply kept before it", m, ok)
	}
}

// B4: two hundred other threads had something written to them after the
// person replied, from the app, to a stopped session's thread. The thread
// list, newest first, gives them on its first page and the session's
// thread on the next. The sweep resumes the session.
func TestK1ASweepFindsAReplyPastTheNewestTwoHundredThreads(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "m1", "is the deploy done?", stop.Add(time.Minute), nil)
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var pages atomic.Int32
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/threads" || r.URL.Query().Get("cursor") == "page-2" {
			proxy.ServeHTTP(w, r)
			return
		}
		pages.Add(1)
		var others []map[string]any
		for i := 0; i < 200; i++ {
			others = append(others, map[string]any{"id": fmt.Sprint("o", i), "external_id": fmt.Sprint("other:", i), "preview_sender": "user", "last_activity_at": time.Now()})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"threads": others, "next_cursor": "page-2"})
	}))
	t.Cleanup(front.Close)
	host := &fakeResumeHost{hosted: map[string]bool{}}
	w := f.watcher(project, host, t)
	w.client = &finalechat.Client{BaseURL: front.URL, Token: "fc_fixture"}
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || pages.Load() != 1 {
		t.Fatalf("resumed %#v after %d first pages", host.resumed, pages.Load())
	}
}
