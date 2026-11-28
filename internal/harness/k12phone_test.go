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

// As TestSR3PhoneAMessageGivenUpAtAResumeIsNotTakenWithItsResend, but the
// run is stopped (Ctrl-C right after the resume, say) while its read of
// what waited is still out, and that read then fails for good. The thread
// is told the message was given up and to send it again; the cursor that
// gives it up is posted to a loop that has closed, so it is never written.
// The next run then takes the given-up message as well as its resend.
func TestSR4PhoneGivenUpAsTheRunStopsIsNotTakenWithItsResend(t *testing.T) {
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
	rtc := make(chan *Runtime, 1)
	reading := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") && failed.CompareAndSwap(false, true) {
			close(reading)
			rt := <-rtc
			rtc <- rt
			<-rt.closed // the run's loop has closed
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
	_, stop := bphoneRun(t, func() *Runtime {
		useFakeRelays(t, newFakeSeat("Done."))
		rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
		if err != nil {
			t.Fatal(err)
		}
		rtc <- rt
		return rt
	})
	<-reading
	stop() // the person stops the session while the read is out
	told := notice()
	if got := r6PhoneIDs(path); len(got) != 0 {
		t.Fatalf("setup: the run took %v", got)
	}
	id, ok := bphoneAnchor(path)
	t.Logf("the stopped run's cursor: %q, written %v; thread told %q", id, ok, told)
	if told != "" && !ok {
		t.Fatalf("the thread was told %q, but no cursor gives the message up", told)
	}
	// As the thread asked, if it was told: sent again.
	resent := fp.userSays("the checklist is A, B, C", nil)
	cfg.Finalechat.BaseURL = fp.srv.URL
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
	if got := r6PhoneIDs(path); told != "" && slices.Contains(got, first) {
		t.Fatalf("the thread was told %q, and the person sent the words again; the next run took both (%v): the same message reached the session twice", told, got)
	}
}
