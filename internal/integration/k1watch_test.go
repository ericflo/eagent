package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// k1Files stands in front of an r6Thread fixture: its downloads answer 502
// while fails lasts (each takes one), and it counts the downloads and keeps
// the bodies posted.
type k1Files struct {
	srv       *httptest.Server
	fails     atomic.Int32
	downloads atomic.Int32
	mu        sync.Mutex
	notes     []string
}

func newK1Files(t *testing.T, f *r6Thread, fails int32) *k1Files {
	t.Helper()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	fr := &k1Files{}
	fr.fails.Store(fails)
	fr.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/files/") {
			fr.downloads.Add(1)
			if fr.fails.Add(-1) >= 0 {
				http.Error(w, "upstream unavailable", http.StatusBadGateway)
				return
			}
		}
		if r.Method == http.MethodPost {
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

func (fr *k1Files) posted() []string {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return append([]string(nil), fr.notes...)
}

// B12: the download of a file sent with a reply to a stopped session fails
// once with a 502. It is tried again, and the session comes back with the
// file; nobody is told it was refused.
func TestK1AFileWhoseDownloadFailedOnceIsTriedAgain(t *testing.T) {
	_, _, f, _, host, w := r9Stopped(t, "see the screenshot", r9Shot())
	fr := newK1Files(t, f, 1)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].Text != "see the screenshot" || len(host.resumed[0].Attachments) != 1 {
		t.Fatalf("resumed %#v, want once with the words and the file", host.resumed)
	}
	if notes := fr.posted(); len(notes) != 0 {
		t.Fatalf("posted %q for a file that came", notes)
	}
}

// B12, its bound: a file whose download always fails with a 502 is tried
// three times, as the client tries a post, and then refused: the sender is
// told, and the session comes back with the words.
func TestK1AFileWhoseDownloadAlwaysFailsIsRefusedAfterThreeTries(t *testing.T) {
	_, _, f, _, host, w := r9Stopped(t, "see the screenshot", r9Shot())
	fr := newK1Files(t, f, 1000)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].Text != "see the screenshot" || len(host.resumed[0].Attachments) != 0 {
		t.Fatalf("resumed %#v, want once with the words", host.resumed)
	}
	if n := fr.downloads.Load(); n != 3 {
		t.Fatalf("%d downloads, want three", n)
	}
	if notes := fr.posted(); len(notes) != 1 || !strings.Contains(notes[0], "shot.png") {
		t.Fatalf("the sender was not told once: %q", notes)
	}
}

// B11: a serve restart finds a reply with two files too large to take. The
// notice about the first goes out, the one about the second cannot: the
// restore takes nothing, and eagent's notice is the thread's latest
// message, which the sweep passes over. The reply watcher, started after
// the restore as the serve starts it, resumes the session with the words,
// the sender told about each file.
func TestK1ARestoreWhoseSecondNoticeFailedLeavesTheReplyToTheWatcher(t *testing.T) {
	big2 := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip2.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	project, sid, f, fr, stopped, _ := r11Restore(t, big2)
	host := &k1Host{ResumeHost: stopped, next: make(chan PhoneMessage, 4)}
	fr.failPosts(0, http.StatusForbidden)
	if msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid)); err == nil || ok {
		t.Fatalf("taken while nobody could be told about clip2.mov: msg %#v ok=%v err=%v", msg, ok, err)
	}
	if p := r10Preview(f, sid); p != "system" {
		t.Fatalf("the thread's latest message is from %q, want the first notice", p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := StartResumeWatcher(ctx, project, host, t.Logf)
	defer stop()
	defer cancel()
	m, ok := k1Next(t, host)
	if !ok || m.ID != "m1" || m.Text != "please look at both" || len(m.Attachments) != 0 {
		t.Fatalf("resumed %#v (%v), want the words", m, ok)
	}
	if n := r11Told(fr, "clip2.mov"); n != 1 {
		t.Fatalf("%d notices about clip2.mov: %q", n, fr.posted())
	}
}

// Batch 2, the stream held: a reply comes on the event stream (no
// deadline of the sweep's) with a file whose download stalls. The
// message's files hold the stream for at most the time one download gets:
// the file is refused, the sender told, and the session comes back with
// the words.
func TestK1AStalledFileHoldsTheStreamForOneDownloadAtMost(t *testing.T) {
	_, sid, f, _, host, w := r9Stopped(t, "see the screenshot", r9Shot())
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	release := make(chan struct{})
	var notes atomic.Int32
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/files/") {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		if r.Method == http.MethodPost {
			notes.Add(1)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	w.client = &finalechat.Client{BaseURL: front.URL, Token: "fc_fixture"}
	defer func(b time.Duration) { fetchBudget = b }(fetchBudget)
	fetchBudget = 100 * time.Millisecond
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.consider(context.Background(), finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}, finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "see the screenshot", CreatedAt: time.Now()})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(release)
		<-done
		t.Fatal("a stalled file held the stream past the time of one download")
	}
	close(release)
	if host.calls() != 1 || host.resumed[0].Text != "see the screenshot" || len(host.resumed[0].Attachments) != 0 || notes.Load() != 1 {
		t.Fatalf("resumed %#v with %d notes, want the words and the sender told", host.resumed, notes.Load())
	}
}

// Batch 2, the day's cutoff: a reply kept quietly for a session that has
// run for more than a day is written down at a stop of the serve and taken
// up by the next serve's watcher. It keeps the time it was first kept, so
// the cutoff lets it go.
func TestK1AKeptReplyKeepsItsFirstTimeAcrossARestart(t *testing.T) {
	project, sid, _, f, host, w := r9Running(t, true)
	thread := finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}
	w.retries.replies["m1"] = pendingReply{thread: thread, msg: finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "one more thing"}, since: time.Now().Add(-25 * time.Hour)}
	w.retries.writeDown(project, t.Logf)
	next := f.watcher(project, host, t)
	next.sweep(context.Background())
	if n := len(next.retries.replies); n != 0 {
		t.Fatalf("kept after more than a day: %#v", next.retries.replies)
	}
}

// Batch 2, two processes: the serve's watcher and `connector run`'s stop
// at once, each with a reply kept quietly for a thread of its own. The
// next watcher takes up both.
func TestK1TwoWatchersStoppingAtOnceKeepBothReplies(t *testing.T) {
	for i := 0; i < 5; i++ {
		project := t.TempDir()
		var a, b replyRetries
		a.replies = map[string]pendingReply{"m1": {thread: finalechat.Thread{ExternalID: "eagent:s1"}, msg: finalechat.Message{ID: "m1"}, since: time.Now()}}
		b.replies = map[string]pendingReply{"m2": {thread: finalechat.Thread{ExternalID: "eagent:s2"}, msg: finalechat.Message{ID: "m2"}, since: time.Now()}}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, r := range []*replyRetries{&a, &b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				r.writeDown(project, t.Logf)
			}()
		}
		close(start)
		wg.Wait()
		next := replyRetries{replies: map[string]pendingReply{}}
		next.takeUp(project)
		if _, ok1 := next.replies["m1"]; !ok1 || len(next.replies) != 2 {
			t.Fatalf("round %d: took up %#v, want both replies", i, next.replies)
		}
	}
}

// Batch 2, the watch held elsewhere: as in B14, a serve restart brings a
// session back with a /quit and a message after it, but `connector run`
// holds the project's watch, so the serve's own watcher never sweeps. The
// connector's watcher resumes the session with the message after the
// /quit.
func TestK1ARestoreLeavesTheQuitsFollowerToWhoeverHoldsTheWatch(t *testing.T) {
	sid, f, quit, _ := r13QuitWaiting(t, "/quit", "actually fix the login bug")
	t.Setenv("FINALECHAT_URL", f.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	msg, ok, err := PendingReply(context.Background(), quit.project, r6Info(t, quit.project, sid))
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("pending reply %#v ok=%v err=%v, want the /quit", msg, ok, err)
	}
	if err := quit.Resume(sid, msg); err != nil {
		t.Fatal(err)
	}
	connector := f.watcher(quit.project, quit, t)
	connector.sweep(context.Background())
	if got := r13Resumed(quit); len(got) != 2 || got[1] != "m2" {
		t.Fatalf("resumed %v, want the /quit and then the message after it", got)
	}
}

// Batch 2, the log: a reply reaches a running session, and the terminal
// says once that it is left to the session's mirror; the sweeps that look
// at the reply kept for when the session stops say nothing more.
func TestK1AKeptReplyIsLeftToTheMirrorInOneLogLine(t *testing.T) {
	_, sid, _, f, _, w := r9Running(t, true)
	var lines atomic.Int32
	w.logf = func(format string, args ...any) {
		if strings.Contains(format, "left to its running mirror") {
			lines.Add(1)
		}
	}
	r9Says(w, f, sid, "m1", "one more thing")
	w.sweep(context.Background())
	w.sweep(context.Background())
	if n := lines.Load(); n != 1 || len(w.retries.replies) != 1 {
		t.Fatalf("%d lines, kept %#v; want one line and the reply kept", n, w.retries.replies)
	}
}

// Batch 2, the promise: a reply could not resume its session, and the
// thread was told eagent keeps trying every minute; that note is the
// thread's latest message, which the sweep passes over. The serve
// restarts. The next serve's watcher keeps trying, resumes the session
// with the reply, and does not tell the thread a second time.
func TestK1AReplyWhoseResumeFailedIsTriedAgainAfterARestart(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	r6Record(t, project, sid, r6Run(), r6Read("start"))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "start", "", stop.Add(-time.Hour), nil)
	f.mu.Lock()
	f.threads[sid][0]["origin"] = "token"
	f.mu.Unlock()
	f.say(sid, "m1", "please deploy the fix", stop.Add(time.Minute), nil)
	fr := newR10Front(t, f)
	host := &flakyResumeHost{fakeResumeHost: fakeResumeHost{hosted: map[string]bool{}}, failures: 1}
	w := f.watcher(project, host, t)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	w.sweep(context.Background())
	if p := w.retries.replies["m1"]; host.calls() != 0 || !p.told || r10Preview(f, sid) != "system" {
		t.Fatalf("after the failed resume: resumed %#v, kept %#v, latest from %s", host.resumed, w.retries.replies, r10Preview(f, sid))
	}
	w.retries.writeDown(project, t.Logf)
	next := f.watcher(project, host, t)
	next.client = w.client
	next.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" {
		t.Fatalf("after the restart: resumed %#v", host.resumed)
	}
	if n := r11Told(fr, "could not resume"); n != 1 {
		t.Fatalf("told %d times: %q", n, fr.posted())
	}
}

// k1BlockKept makes the project's kept file impossible to write: its
// directory is a plain file.
func k1BlockKept(t *testing.T, project string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(stateDir(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateDir(project), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Batch 2, the hand-over that fails: a serve restart finds /quit and a
// message after it, and the reply cannot be written down for the watcher.
// The restore gets the error (and says so at the terminal) instead of
// resuming the session with nothing left to look again; nothing was
// posted, so the reply is still the thread's latest message, and the
// watcher's sweeps resume the session with the /quit and then with the
// message after it.
func TestK1ARestoreWhoseHandOverFailsReturnsTheErrorAndLeavesTheReply(t *testing.T) {
	sid, f, quit, w := r13QuitWaiting(t, "/quit", "actually fix the login bug")
	t.Setenv("FINALECHAT_URL", f.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	k1BlockKept(t, quit.project)
	if msg, ok, err := PendingReply(context.Background(), quit.project, r6Info(t, quit.project, sid)); !errors.Is(err, ErrHandOver) || ok {
		t.Fatalf("pending reply %#v ok=%v err=%v, want the hand-over's error", msg, ok, err)
	}
	if p := r10Preview(f, sid); p != "user" {
		t.Fatalf("the thread's latest message is from %q, want the reply", p)
	}
	w.sweep(context.Background())
	w.sweep(context.Background())
	if got := r13Resumed(quit); len(got) != 2 || got[0] != "m1" || got[1] != "m2" {
		t.Fatalf("resumed %v, want the /quit and then the message after it", got)
	}
}

// Batch 2, the same on the other hand-over: the first notice about two
// files too large goes, the second does not, and the reply cannot be
// written down either. The error the restore gets says both.
func TestK1ARestoreWhoseNoticeAndHandOverFailSaysBoth(t *testing.T) {
	big2 := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip2.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	project, sid, _, fr, _, _ := r11Restore(t, big2)
	fr.failPosts(0, http.StatusForbidden)
	k1BlockKept(t, project)
	_, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
	if ok || err == nil || !strings.Contains(err.Error(), "not a directory") || !strings.Contains(err.Error(), "refused") || errors.Is(err, ErrHandOver) {
		t.Fatalf("ok=%v err=%v, want the notice's error and the hand-over's", ok, err)
	}
}

// Batch 2, the window: a serve restart finds /quit and a message after it
// in a thread whose latest message is eagent's (the sweep's own pass does
// not look at it), and `connector run` holds the watch. Its watcher sweeps
// after PendingReply has handed the reply over and before the restore
// starts the session: it leaves the session to the restore, which resumes
// it with the /quit, and then resumes it with the message after it. The
// /quit is taken once.
func TestK1AWatcherElsewhereLeavesARestoredReplyToTheRestore(t *testing.T) {
	sid, f, quit, _ := r13QuitWaiting(t, "/quit", "actually fix the login bug")
	f.mu.Lock()
	f.threads[sid] = append(f.threads[sid], map[string]any{"id": "note", "sender": "system", "origin": "token", "body": "eagent session ended (shutdown).", "created_at": time.Now(), "meta": map[string]any{}})
	f.mu.Unlock()
	t.Setenv("FINALECHAT_URL", f.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	msg, ok, err := PendingReply(context.Background(), quit.project, r6Info(t, quit.project, sid))
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("pending reply %#v ok=%v err=%v, want the /quit", msg, ok, err)
	}
	connector := f.watcher(quit.project, quit, t)
	connector.sweep(context.Background())
	if err := quit.Resume(sid, msg); err != nil {
		t.Fatal(err)
	}
	connector.sweep(context.Background())
	if got := r13Resumed(quit); len(got) != 2 || got[0] != "m1" || got[1] != "m2" {
		t.Fatalf("resumed %v, want the /quit once and then the message after it", got)
	}
}
