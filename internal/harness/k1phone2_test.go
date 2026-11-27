package harness

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// c328c06: the read that finds what waited (after_time / before) fails for
// good every time (a service that refuses it). At f36e19c the poll still
// read on from the session's own post and took what the person wrote after
// the resume; now it takes nothing for the whole run, and says nothing.
func TestRV15ReadOfWhatWaitedAlwaysRefusedTakesNothingEver(t *testing.T) {
	setFor(t, &phoneRetryStep, 10*time.Millisecond)
	cfg, project, path, fp := stoppedRelaySession(t)
	bphoneOlder(t, path)
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") && (q.Has("after_time") || q.Has("before")) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"no"}}`))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg.Finalechat.BaseURL = srv.URL
	rt, stop := bphoneRun(t, func() *Runtime {
		useFakeRelays(t, newFakeSeat("Done."))
		rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
		if err != nil {
			t.Fatal(err)
		}
		return rt
	})
	waitFor(t, "the resumed session's first post", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		for _, p := range fp.posts {
			if p["body"] == "eagent session resumed here." {
				return true
			}
		}
		return false
	})
	later := fp.userSays("run the tests", nil)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && !slices.Contains(r6PhoneIDs(path), later) {
		time.Sleep(50 * time.Millisecond)
	}
	rt.sync(func() {})
	stop()
	if !slices.Contains(r6PhoneIDs(path), later) {
		t.Fatalf("a message written 8s after the resume was never taken: recorded %v", r6PhoneIDs(path))
	}
}
