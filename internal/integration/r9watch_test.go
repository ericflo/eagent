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

	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/store"
)

// r9Host resumes as the harness does: the message it is resumed with is
// recorded in the session's ledger as taken in, files or not.
type r9Host struct {
	fakeResumeHost
	t       *testing.T
	project string
}

func (h *r9Host) Resume(id string, msg PhoneMessage) error {
	r6Record(h.t, h.project, id, r6Read(msg.ID))
	return h.fakeResumeHost.Resume(id, msg)
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

// Finding 2: a screenshot sent alone whose download fails for a moment
// does not resume the session without it (which would record the message
// as taken in, and lose it): the reply waits for the next sweep, which
// resumes the session with the file.
func TestAFileThatCouldNotBeFetchedYetWaitsForTheNextSweep(t *testing.T) {
	project, sid, _, fr, host, w := r9Stopped(t, "", r9Shot())
	fr.files.Store(http.StatusBadGateway)
	w.sweep(context.Background())
	if host.calls() != 0 {
		t.Fatalf("resumed while its file could not be fetched: %#v", host.resumed)
	}
	if r9Taken(t, project, sid, "m1") {
		t.Fatal("the message was recorded as taken in without its file")
	}
	if _, kept := w.retries.replies["m1"]; !kept {
		t.Fatal("the reply was not kept for the next sweep")
	}
	for _, n := range fr.posted() {
		if strings.Contains(n, "did not take") {
			t.Fatalf("told the sender the file was refused: %q", n)
		}
	}
	fr.files.Store(0)
	w.sweep(context.Background())
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || len(host.resumed[0].Attachments) != 1 {
		t.Fatalf("after the download works: %#v", host.resumed)
	}
}

// Finding 2: words and a file, the file's download failing for a moment:
// the session is not resumed with the words and the file missing.
func TestWordsAndAFileThatCouldNotBeFetchedYetWaitTogether(t *testing.T) {
	_, _, _, fr, host, w := r9Stopped(t, "see the screenshot", r9Shot())
	fr.files.Store(http.StatusBadGateway)
	w.sweep(context.Background())
	if host.calls() != 0 {
		t.Fatalf("resumed while its file could not be fetched: %#v", host.resumed)
	}
	fr.files.Store(0)
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].Text != "see the screenshot" || len(host.resumed[0].Attachments) != 1 {
		t.Fatalf("after the download works: %#v", host.resumed)
	}
}

// Finding 2: a file too large to take is left out once the sender is told;
// when the notice cannot be posted, the session is not resumed without the
// file yet (nobody would know why it is missing).
func TestAFileTooLargeIsLeftOutOnlyOnceTheSenderIsTold(t *testing.T) {
	big := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	_, _, _, fr, host, w := r9Stopped(t, "here it is", big)
	fr.posts.Store(http.StatusForbidden)
	w.sweep(context.Background())
	if host.calls() != 0 {
		t.Fatalf("resumed without the file and nobody told: %#v", host.resumed)
	}
	fr.posts.Store(0)
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].Text != "here it is" || len(host.resumed[0].Attachments) != 0 {
		t.Fatalf("after the notice could be posted: %#v", host.resumed)
	}
	told := false
	for _, n := range fr.posted() {
		told = told || strings.Contains(n, "clip.mov")
	}
	if !told {
		t.Fatalf("the sender was not told: %q", fr.posted())
	}
}
