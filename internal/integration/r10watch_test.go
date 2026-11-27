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
)

// r10Front stands in front of an r6Thread fixture as FinaleChat does for
// posts: a post is the thread's latest message from then on, so the
// thread's preview names its sender. Downloads fail with files (0 serves
// them); each post takes the next status in fail (0 lets it through), and
// once fail is used up posts go through.
type r10Front struct {
	srv   *httptest.Server
	files atomic.Int32
	mu    sync.Mutex
	fail  []int
	notes []string
}

func newR10Front(t *testing.T, f *r6Thread) *r10Front {
	t.Helper()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	fr := &r10Front{}
	fr.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := fr.files.Load(); s != 0 && strings.HasPrefix(r.URL.Path, "/files/") {
			http.Error(w, "upstream unavailable", int(s))
			return
		}
		if r.Method == http.MethodPost {
			fr.mu.Lock()
			s := 0
			if len(fr.fail) > 0 {
				s, fr.fail = fr.fail[0], fr.fail[1:]
			}
			fr.mu.Unlock()
			if s != 0 {
				http.Error(w, "refused", s)
				return
			}
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			fr.mu.Lock()
			fr.notes = append(fr.notes, fmt.Sprint(body["body"]))
			fr.mu.Unlock()
			sid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/threads/ext:eagent:"), "/messages")
			f.say(sid, fmt.Sprintf("note-%d", len(fr.posted())), fmt.Sprint(body["body"]), time.Now(), nil)
			f.mu.Lock()
			f.threads[sid][len(f.threads[sid])-1]["sender"] = fmt.Sprint(body["sender"])
			f.mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(fr.srv.Close)
	return fr
}

func (fr *r10Front) posted() []string {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return append([]string(nil), fr.notes...)
}

func (fr *r10Front) failPosts(statuses ...int) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	fr.fail = statuses
}

// r10Preview is who the thread's latest message is from, as the sweep
// reads the threads.
func r10Preview(f *r6Thread, sid string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs := f.threads[sid]
	return fmt.Sprint(msgs[len(msgs)-1]["sender"])
}

func r10File(id, name string) map[string]any {
	return map[string]any{"id": id, "kind": "image", "content_type": "image/png", "filename": name, "size": 3, "url": "/files/" + id}
}

// Finding 12A: a reply with two files, neither of which can be fetched.
// The notice about the first goes (the thread's latest message is then
// eagent's), the notice about the second cannot: the reply is not taken
// in, and is kept for the sweeps, which read the thread for it although
// its preview is no longer the user's. Once all is well again the session
// comes back with the reply, once.
func TestR10AReplyWhoseSecondNoticeFailedIsKeptForTheSweeps(t *testing.T) {
	project, sid, f, _, host, w := r9Stopped(t, "please fix the login crash", r9Shot())
	f.mu.Lock()
	f.threads[sid][1]["attachments"] = []map[string]any{r10File("att-1", "one.png"), r10File("att-2", "two.png")}
	f.mu.Unlock()
	fr := newR10Front(t, f)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	fr.files.Store(http.StatusNotFound) // for good, so the sender is told
	fr.failPosts(0, http.StatusForbidden)
	w.sweep(context.Background())
	if host.calls() != 0 || r9Taken(t, project, sid, "m1") {
		t.Fatalf("taken in while nobody could be told about two.png: %#v", host.resumed)
	}
	if p := r10Preview(f, sid); p != "system" {
		t.Fatalf("the thread's latest message is from %q, want the notice about one.png", p)
	}
	fr.files.Store(0)
	for i := 0; i < 3; i++ {
		w.sweep(context.Background())
	}
	if host.calls() != 1 || host.resumed[0].ID != "m1" || host.resumed[0].Text != "please fix the login crash" || len(host.resumed[0].Attachments) != 2 {
		t.Fatalf("after all is well again: resumed %#v, kept %#v", host.resumed, w.retries.replies)
	}
	if n := len(w.retries.replies); n != 0 {
		t.Fatalf("%d replies still kept after the session was resumed: %#v", n, w.retries.replies)
	}
}

// Finding 12B: m2 is kept after a resume that failed (the thread told, its
// latest message eagent's). The retry finds m1, earlier and not taken in,
// whose file cannot be fetched and whose notice cannot be posted: neither
// is dropped. Once all is well again the session comes back with m1 (its
// poll reads m2 after it), once.
func TestR10AnEarlierReplyBehindAKeptOneLosesNeither(t *testing.T) {
	project, sid, f, _, host, w := r9Stopped(t, "first task", r9Shot())
	at := time.Now()
	f.say(sid, "m2", "second task", at, nil)
	f.mu.Lock()
	f.threads[sid] = append(f.threads[sid], map[string]any{"id": "resume-failed", "sender": "system", "origin": "token", "body": "eagent could not resume this session for your reply yet", "created_at": at.Add(time.Second)})
	f.mu.Unlock()
	thread := finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}
	w.retries.replies["m2"] = pendingReply{thread: thread, msg: finalechat.Message{ID: "m2", Sender: "user", Origin: "session", Body: "second task", CreatedAt: at}, since: at, told: true}
	fr := newR10Front(t, f)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	fr.files.Store(http.StatusNotFound) // for good, so the sender is told
	fr.failPosts(http.StatusForbidden)
	w.sweep(context.Background())
	if host.calls() != 0 || r9Taken(t, project, sid, "m1") {
		t.Fatalf("taken in while nobody could be told about shot.png: %#v", host.resumed)
	}
	if _, kept := w.retries.replies["m2"]; !kept {
		t.Errorf("m2 dropped before the session was resumed: kept %#v", w.retries.replies)
	}
	fr.files.Store(0)
	for i := 0; i < 3; i++ {
		w.sweep(context.Background())
	}
	if host.calls() != 1 || host.resumed[0].ID != "m1" || host.resumed[0].Text != "first task" || len(host.resumed[0].Attachments) != 1 {
		t.Fatalf("after all is well again: resumed %#v, kept %#v", host.resumed, w.retries.replies)
	}
}

// Finding 14: restoring a session after a restart of the serve, a file too
// large to take whose notice cannot be posted is not left out untold: no
// reply is taken (the error comes back, as for any other failed download
// there). Once the notice goes, the session comes back with the message
// without the file. With words and without.
func TestR10ARestoreLeavesATooLargeFileOutOnlyOnceTheSenderIsTold(t *testing.T) {
	big := map[string]any{"id": "att-2", "kind": "file", "content_type": "video/quicktime", "filename": "clip.mov", "size": finalechat.MaxAttachmentSize + 1, "url": "/files/att-2"}
	for _, body := range []string{"", "here it is"} {
		t.Run("words="+body, func(t *testing.T) {
			project, sid, _, fr, _, _ := r9Stopped(t, body, big)
			t.Setenv("FINALECHAT_URL", fr.srv.URL)
			t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
			fr.posts.Store(http.StatusForbidden) // not retried by the client
			msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
			if err == nil || ok {
				t.Fatalf("taken while nobody could be told: msg %#v ok=%v err=%v", msg, ok, err)
			}
			fr.posts.Store(0)
			msg, ok, err = PendingReply(context.Background(), project, r6Info(t, project, sid))
			if err != nil || !ok || msg.ID != "m1" || msg.Text != body || len(msg.Attachments) != 0 {
				t.Fatalf("once the notice can go: msg %#v ok=%v err=%v", msg, ok, err)
			}
			told := false
			for _, n := range fr.posted() {
				told = told || strings.Contains(n, "clip.mov")
			}
			if !told {
				t.Fatalf("the sender was not told: %q", fr.posted())
			}
		})
	}
}
