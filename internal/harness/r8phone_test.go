package harness

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// r8BeforeMirror stands in front of the phone service and runs before once,
// just before the first post that mirrors a message of the user's.
func r8BeforeMirror(t *testing.T, target string, before func()) string {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var ran atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if bytes.Contains(body, []byte(`"eagent":"mirror"`)) && ran.CompareAndSwap(false, true) {
				before()
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Finding 3 of the eighth review: a session with no phone cursor is resumed
// at the terminal with an answer (--answer), which is recorded before the
// mirror starts and so is mirrored before the poll does. A reply written in
// the app after the check for waiting messages and before that mirror post
// is taken, and so is the one after it, each once and in order.
func TestAReplyWrittenWhileTheTerminalWordsAreMirroredIsTaken(t *testing.T) {
	cfg, project, path, fp := stoppedRelaySession(t)
	bphoneOlder(t, path)
	var first string
	cfg.Finalechat.BaseURL = r8BeforeMirror(t, fp.srv.URL, func() {
		first = fp.userSays("one thing before you go on", nil)
	})
	polls := fp.getCount()
	rt, stop := bphoneRun(t, func() *Runtime {
		useFakeRelays(t, newFakeSeat("Done."))
		rt, err := Resume(cfg, Options{Project: project, Interactive: true, Answer: "carry on with the login test"}, &fakeUI{input: make(chan string, 4)}, path)
		if err != nil {
			t.Fatal(err)
		}
		return rt
	})
	waitFor(t, "the live poll", func() bool { return fp.getCount() > polls+1 })
	second := fp.userSays("and one more", nil)
	waitFor(t, "the later reply in the record", func() bool {
		ids := r6PhoneIDs(path)
		return len(ids) > 0 && ids[len(ids)-1] == second
	})
	rt.sync(func() {})
	stop()
	if got := r6PhoneIDs(path); len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("recorded %v, want %v", got, []string{first, second})
	}
}
