package integration

import (
	"context"
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

// r12Stall stands in front of target; while stall is set, a file download
// gets no answer until its caller gives up, and started is closed when
// the first one comes.
func r12Stall(t *testing.T, target string) (front string, stall *atomic.Bool, started <-chan struct{}) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	stall = &atomic.Bool{}
	stall.Store(true)
	first := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/files/") && stall.Load() {
			once.Do(func() { close(first) })
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, stall, first
}

// Finding 13 (twelfth review): a download that the watcher's own stop
// cuts off (eagent serve stopping) is no refusal: nothing is posted and
// the session is not resumed. The reply is still the thread's latest, so
// the next sweep (here; or the next serve's) takes it, with its file and
// nobody told anything. The watcher used to post "could not fetch
// shot.png (... context canceled)" and resume the session without it.
func TestR12AFetchCutOffByTheWatchersStopIsNoRefusal(t *testing.T) {
	_, sid, f, fr, host, w := r9Stopped(t, "see the screenshot", r9Shot())
	front, stall, started := r12Stall(t, fr.srv.URL)
	w.client = &finalechat.Client{BaseURL: front, Token: "fc_fixture"}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.consider(ctx, finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}, finalechat.Message{}) // as a sweep does
	}()
	<-started
	stop() // the serve stops mid-download
	<-done
	if host.calls() != 0 || len(fr.posted()) != 0 {
		t.Fatalf("after the stop cut the download off: resumed %#v, posted %q", host.resumed, fr.posted())
	}
	if p := r10Preview(f, sid); p != "user" {
		t.Fatalf("the thread's latest message is from %q, want the reply", p)
	}
	stall.Store(false)
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || len(host.resumed[0].Attachments) != 1 || len(fr.posted()) != 0 {
		t.Fatalf("the next sweep: resumed %#v, posted %q", host.resumed, fr.posted())
	}
}
