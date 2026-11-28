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
// three times, as the client tries a post, and then refused: the session
// comes back with the words, the file refused on the resume for the
// resumed session to tell the sender.
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
	if r := host.resumed[0].Refused; len(r) != 1 || !strings.Contains(r[0].Text, "shot.png") || len(fr.posted()) != 0 {
		t.Fatalf("refused %#v, posted %q; want shot.png refused once and nothing posted", r, fr.posted())
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
	if host.calls() != 1 || host.resumed[0].Text != "see the screenshot" || len(host.resumed[0].Attachments) != 0 || !k3Refused(host.resumed[0], "shot.png") || notes.Load() != 0 {
		t.Fatalf("resumed %#v with %d notes, want the words and shot.png refused on the resume", host.resumed, notes.Load())
	}
}

// Batch 2, the log: a reply reaches a running session, and the terminal
// says once that it is left to the session's mirror; the sweeps that look
// at the reply kept for when the session stops say nothing more.
func TestK1AKeptReplyIsLeftToTheMirrorInOneLogLine(t *testing.T) {
	_, sid, _, f, _, w := r9Running(t, true)
	var lines atomic.Int32
	w.logf = func(format string, args ...any) {
		if format == leftToItsRun {
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

// k1BrowserFirst is the serve when the browser resumed the session first:
// by the time the watcher's resume comes, the session is hosted, and the
// serve answers that it is running (web.ErrRunning).
type k1BrowserFirst struct{ fakeResumeHost }

func (h *k1BrowserFirst) Resume(id string, msg PhoneMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hosted[id] = true
	return errors.New("the session is running; its own mirror reads the thread")
}

// Batch 2, the browser first: a reply reaches a stopped session's thread
// as the browser resumes the session. The watcher's resume finds it
// running: as when its own check finds a session running, the reply is
// left to that run's mirror and kept quietly for when it stops; the thread
// is not told eagent could not resume the session, and the reply is not
// claimed.
func TestK1AReplyToASessionTheBrowserResumedFirstIsLeftToItsMirror(t *testing.T) {
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
	host := &k1BrowserFirst{fakeResumeHost{hosted: map[string]bool{}}}
	w := f.watcher(project, host, t)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	w.consider(context.Background(), finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}, finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "please deploy the fix", CreatedAt: time.Now()})
	if notes := fr.posted(); len(notes) != 0 {
		t.Fatalf("posted %q", notes)
	}
	if p, kept := w.retries.replies["m1"]; !kept || p.told {
		t.Fatalf("kept %#v, want the reply kept quietly", w.retries.replies)
	}
	if _, claimed := w.handled["m1"]; claimed {
		t.Fatal("the reply is claimed")
	}
}
