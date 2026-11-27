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

	"github.com/ericflo/eagent/internal/finalechat"
)

// rv9Hang stands in front of an r6Thread fixture; while hang is set, a file
// download gets no answer (a stalled file store) until the client gives up.
type rv9Hang struct {
	srv   *httptest.Server
	hang  atomic.Bool
	files atomic.Int32
	posts atomic.Int32
}

func newRv9Hang(t *testing.T, f *r6Thread) *rv9Hang {
	t.Helper()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	h := &rv9Hang{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/files/") {
			h.files.Add(1)
			if h.hang.Load() {
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
				return
			}
		}
		if r.Method == http.MethodPost {
			h.posts.Add(1)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

// The sweep's deadline (here shortened: the parent's) cuts a stalled
// download off. Reply m1 reached the session while it was stopping, was kept,
// and the session stopped without taking it; its closing note is the
// thread's latest. The first sweep after the stop is cut off mid-download;
// the reply is not lost: as the running mirror does, the session comes
// back with the words, once, the file refused on the resume for the
// resumed session to tell the sender.
func TestRv9AKeptReplyCutOffMidDownloadStillResumesLater(t *testing.T) {
	_, sid, sess, f, host, w := r9Running(t, true)
	at := time.Now()
	f.say(sid, "m1", "see the screenshot", at, nil)
	f.mu.Lock()
	f.threads[sid][len(f.threads[sid])-1]["attachments"] = []map[string]any{r9Shot()}
	f.mu.Unlock()
	front := newRv9Hang(t, f)
	w.client = &finalechat.Client{BaseURL: front.srv.URL, Token: "fc_fixture"}
	w.consider(context.Background(), finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}, finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "see the screenshot", CreatedAt: at})
	if len(w.retries.replies) != 1 {
		t.Fatalf("not kept while running: %#v", w.retries.replies)
	}
	r9Ends(t, sess, f, sid)

	front.hang.Store(true)
	cut, stop := context.WithTimeout(context.Background(), 300*time.Millisecond)
	w.sweep(cut) // as the 60-second deadline cuts a stalled download off
	stop()
	if front.files.Load() == 0 {
		t.Fatal("the sweep never tried the file")
	}

	front.hang.Store(false)
	w.sweep(context.Background())
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || !k3Refused(host.resumed[0], "shot.png") {
		t.Fatalf("reply m1 lost after one cut-off download: resumed %#v, kept %#v, posts %d", host.resumed, w.retries.replies, front.posts.Load())
	}
}

// Control for the first: with no cut-off sweep, the kept reply with its
// file resumes the session.
func TestRv9AKeptReplyWithAFileResumesWhenNotCutOff(t *testing.T) {
	_, sid, sess, f, host, w := r9Running(t, true)
	at := time.Now()
	f.say(sid, "m1", "see the screenshot", at, nil)
	f.mu.Lock()
	f.threads[sid][len(f.threads[sid])-1]["attachments"] = []map[string]any{r9Shot()}
	f.mu.Unlock()
	front := newRv9Hang(t, f)
	w.client = &finalechat.Client{BaseURL: front.srv.URL, Token: "fc_fixture"}
	w.consider(context.Background(), finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}, finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "see the screenshot", CreatedAt: at})
	r9Ends(t, sess, f, sid)
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || len(host.resumed[0].Attachments) != 1 {
		t.Fatalf("resumed %#v", host.resumed)
	}
}

// A stopped session whose thread's latest message is a reply with words and
// a file the store never answers for. At 12aec43 the sweep resumed with the
// words; the stream path does so too (Download's own 120 s limit ends the
// fetch, the sender is told). The sweep's 60-second deadline comes first, so
// now each sweep fetches again, nobody is told and the words never come in.
func TestRv9AStalledFileHoldsTheWordsBackEverySweepUntold(t *testing.T) {
	_, sid, f, _, host, w := r9Stopped(t, "see the screenshot", r9Shot())
	front := newRv9Hang(t, f)
	w.client = &finalechat.Client{BaseURL: front.srv.URL, Token: "fc_fixture"}
	front.hang.Store(true)
	for i := 0; i < 3; i++ {
		cut, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
		w.sweep(cut)
		stop()
	}
	_ = sid
	if host.calls() == 0 && front.posts.Load() == 0 {
		t.Fatalf("after %d sweeps: %d downloads, not resumed, nobody told (12aec43 resumed with the words)", 3, front.files.Load())
	}
}
