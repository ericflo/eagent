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

// Finding 12A, under the rule that notices come only from the session that
// takes the reply (a later review proved that keeping the reply here made
// a notice false once the files came): a reply with two files, neither of
// which can be fetched, while the service refuses a post. The session is
// resumed with the words once, both files refused on the resume, for the
// resumed session to tell the sender; the watcher posts nothing.
func TestR10AReplyWhoseSecondNoticeFailedIsTakenWithoutItsFiles(t *testing.T) {
	project, sid, f, _, host, w := r9Stopped(t, "please fix the login crash", r9Shot())
	f.mu.Lock()
	f.threads[sid][1]["attachments"] = []map[string]any{r10File("att-1", "one.png"), r10File("att-2", "two.png")}
	f.mu.Unlock()
	fr := newR10Front(t, f)
	w.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	fr.files.Store(http.StatusBadGateway)
	fr.failPosts(0, http.StatusForbidden)
	w.sweep(context.Background())
	fr.files.Store(0)
	for i := 0; i < 3; i++ {
		w.sweep(context.Background())
	}
	if host.calls() != 1 || host.resumed[0].ID != "m1" || host.resumed[0].Text != "please fix the login crash" || len(host.resumed[0].Attachments) != 0 || !r9Taken(t, project, sid, "m1") {
		t.Fatalf("resumed %#v, kept %#v; want the words once, without the files", host.resumed, w.retries.replies)
	}
	if !k3Refused(host.resumed[0], "one.png") || !k3Refused(host.resumed[0], "two.png") || len(fr.posted()) != 0 {
		t.Fatalf("refused %#v, posted %q; want both refused on the resume and nothing posted", host.resumed[0].Refused, fr.posted())
	}
	// One quiet entry stays, for when the session stops again; the thread
	// is told nothing more.
	if p := w.retries.replies["m1"]; len(w.retries.replies) != 1 || p.msg.ID != "m1" || p.told {
		t.Fatalf("kept after the session was resumed: %#v", w.retries.replies)
	}
}

// Finding 12B: m2 is kept after a resume that failed (the thread told, its
// latest message eagent's). The retry finds m1, earlier and not taken in,
// whose file cannot be fetched while the service refuses posts: the
// session comes back with m1 once, its file refused on the resume (the
// resumed session tells the sender), and its poll reads m2 after it.
// Neither is dropped.
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
	fr.files.Store(http.StatusBadGateway)
	fr.failPosts(http.StatusForbidden)
	w.sweep(context.Background())
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || host.resumed[0].Text != "first task" || len(host.resumed[0].Attachments) != 0 || !k3Refused(host.resumed[0], "shot.png") || !r9Taken(t, project, sid, "m1") {
		t.Fatalf("resumed %#v, kept %#v; want m1 once, shot.png refused", host.resumed, w.retries.replies)
	}
	if _, kept := w.retries.replies["m2"]; kept {
		t.Errorf("m2 still kept, though the session's poll reads it after m1: %#v", w.retries.replies)
	}
	if posts := fr.posted(); len(posts) != 0 {
		t.Fatalf("the watcher posted %q", posts)
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
