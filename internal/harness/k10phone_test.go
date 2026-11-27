package harness

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A session last run without the ledger (an older eagent) is resumed at
// the terminal while a message from the phone waits, and the read that
// finds it fails for good. The thread is told "eagent could not read what
// was sent here while the session was stopped, and goes on from here
// without it. Send again anything it should still see." The run takes
// nothing else and stops; the person sends the message again, as told.
// The next run must not take both: the first was given up, as the thread
// was told.
func TestSR3PhoneAMessageGivenUpAtAResumeIsNotTakenWithItsResend(t *testing.T) {
	setFor(t, &phoneRetryStep, 10*time.Millisecond)
	cfg, project, path, fp := stoppedRelaySession(t)
	bphoneOlder(t, path)
	first := fp.userSays("the checklist is A, B, C", nil)
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var failed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") && failed.CompareAndSwap(false, true) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"not now"}}`))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg.Finalechat.BaseURL = srv.URL
	notice := func() string {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		for _, p := range fp.posts {
			if meta, _ := p["meta"].(map[string]any); meta["kind"] == "unread" {
				body, _ := p["body"].(string)
				return body
			}
		}
		return ""
	}
	rt, stop := bphoneRun(t, func() *Runtime {
		useFakeRelays(t, newFakeSeat("Done."))
		rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
		if err != nil {
			t.Fatal(err)
		}
		return rt
	})
	waitFor(t, "the thread to be told", func() bool { return notice() != "" })
	rt.sync(func() {})
	stop()
	if got := r6PhoneIDs(path); len(got) != 0 {
		t.Fatalf("setup: the run took %v", got)
	}
	// As the thread asked: sent again.
	resent := fp.userSays("the checklist is A, B, C", nil)
	rt3, stop3 := bphoneRun(t, func() *Runtime {
		useFakeRelays(t, newFakeSeat("Done."))
		rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
		if err != nil {
			t.Fatal(err)
		}
		return rt
	})
	waitFor(t, "the resent message in the record", func() bool { return slices.Contains(r6PhoneIDs(path), resent) })
	rt3.sync(func() {})
	stop3()
	if got := r6PhoneIDs(path); slices.Contains(got, first) {
		t.Fatalf("the thread was told %q, and the person sent the words again; the next run took both (%v): the same message reached the session twice", notice(), got)
	}
}
