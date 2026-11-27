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

// A reply from the event stream with two files, each of whose downloads
// takes 70% of the time one download gets (fetchBudget stands in, scaled,
// for Download's own 120 s). At f36e19c each file had its own limit and
// both came; with one budget for the message's files the second is refused
// and the sender told to send it again.
func TestRV15TwoSlowFilesEachUnderOneDownloadsLimitBothCome(t *testing.T) {
	_, sid, f, _, host, w := r9Stopped(t, "see both", r9Shot())
	f.mu.Lock()
	f.threads[sid][1]["attachments"] = []map[string]any{
		r9Shot(),
		{"id": "att-2", "kind": "image", "content_type": "image/png", "filename": "shot2.png", "size": 3, "url": "/files/att-2"},
	}
	f.mu.Unlock()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var notes atomic.Int32
	front := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/files/") {
			select {
			case <-time.After(140 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		if r.Method == http.MethodPost {
			notes.Add(1)
		}
		proxy.ServeHTTP(rw, r)
	}))
	t.Cleanup(front.Close)
	w.client = &finalechat.Client{BaseURL: front.URL, Token: "fc_fixture"}
	defer func(b time.Duration) { fetchBudget = b }(fetchBudget)
	fetchBudget = 200 * time.Millisecond
	w.consider(context.Background(), finalechat.Thread{ID: "t-" + sid, ExternalID: "eagent:" + sid}, finalechat.Message{ID: "m1", Sender: "user", Origin: "session", Body: "see both", CreatedAt: time.Now()})
	if host.calls() != 1 {
		t.Fatalf("resumed %d times, want once", host.calls())
	}
	if n := len(host.resumed[0].Attachments); n != 2 || notes.Load() != 0 {
		t.Fatalf("resumed with %d of 2 files and %d refusal notes; each download took under one download's limit", n, notes.Load())
	}
}
