package harness

import (
	"bytes"
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

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
)

// A message from the phone whose file cannot be fetched, then /quit. The
// words are taken without the file and the note is owed (phone.refused).
// The note's first post meets one 503; while tellRefused waits to try
// again, the /quit ends the session, whose closing note goes out (the
// service is back), and close() cancels the mirror, cutting the note's
// retry. The note was queued on the mirror's one worker before 4b6ceee,
// so the closing note waited for it; now the stopped session owes it
// until it is resumed, which a quit session may never be.
func TestSR6PhoneARefusalNoteOwedAtAQuitIsCutByTheClosingNote(t *testing.T) {
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	u, err := url.Parse(fp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var blipped atomic.Bool
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			if bytes.Contains(raw, []byte("attachment_refused")) && !blipped.Swap(true) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"try later"}}`))
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = front.URL })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	ids := r10Says(fp,
		r10Msg("here is the screenshot of the error", map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}),
		r10Msg("/quit"))
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	taken := false
	for _, ev := range g.log() {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.PhoneID == ids[0] {
			taken = true
		}
	}
	if !taken {
		t.Fatalf("setup: the words were not taken; logs %v", g.ui.logged())
	}
	if !blipped.Load() {
		t.Fatal("setup: the note was never tried")
	}
	owed := owedRefusals(g.log())
	t.Logf("owed after the run: %d; closing notes: %d", len(owed), len(fp.postsWhere(k1Kind("session_end"))))
	if n := len(fp.postsWhere(k1Kind("attachment_refused"))); n != 1 {
		t.Fatalf("the session quit with its refusal note posted %d times (owed on the record: %d); the closing note went out", n, len(owed))
	}
}

// k15Front is upstream behind a front that holds each post of a notice of
// kind until hold is closed (nil: not held), then answers it 503 when
// refuse says so.
func k15Front(t *testing.T, upstream, kind string, hold <-chan struct{}, refuse func() bool) string {
	t.Helper()
	u, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			if bytes.Contains(raw, []byte(`"kind":"`+kind+`"`)) {
				if hold != nil {
					select {
					case <-hold:
					case <-r.Context().Done():
						return
					}
				}
				if refuse != nil && refuse() {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"try later"}}`))
					return
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	return front.URL
}

// A command from the phone reaches a session that is ending (a /quit
// before it in the page), on a service without client keys, and the note
// saying it was not run meets one 503. The note was one post on the
// mirror's worker (notRun), lost to that 503; it is owed as a refused
// file's note is, and goes out once.
func TestK15ACommandNotRunIsToldOnceWhenItsFirstPostFails(t *testing.T) {
	setFor(t, &phoneRetryStep, 10*time.Millisecond)
	fp := newPhoneFake(true)
	fp.features = []string{"activity", "dismiss", "attachments"}
	t.Cleanup(fp.srv.Close)
	var tries atomic.Int32
	front := k15Front(t, fp.srv.URL, "command_not_run", nil, func() bool { return tries.Add(1) == 1 })
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = front })
	waitFor(t, "the agent", func() bool {
		n := 0
		g.rt.sync(func() { n = len(g.rt.st.ActiveRelays()) })
		return n == 1
	})
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	blocked, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) })
	go g.rt.sync(func() { close(blocked); <-release })
	<-blocked
	ids := r10Says(fp, r10Msg("/quit"), r10Msg("/kill"))
	waitFor(t, "the poll to read the page", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		return fp.after == ids[1]
	})
	released.Do(func() { close(release) })
	if code := g.exit(10 * time.Second); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if l := integration.ReadPhoneLedger(g.log()); !l.Taken[ids[1]] {
		t.Fatalf("setup: the command was not taken: %v", l.Taken)
	}
	if tries.Load() == 0 {
		t.Fatal("setup: the note was never tried")
	}
	if n := len(fp.postsWhere(k1Kind("command_not_run"))); n != 1 {
		t.Fatalf("the thread was told %d times that /kill was not run; logs %q", n, g.ui.logged())
	}
	if owed := owedRefusals(g.log()); len(owed) != 0 {
		t.Fatalf("the note went out and is still owed on the record: %v", owed)
	}
}

// On a service without client keys, a refusal note whose post is under
// way as the loop closes (the /quit after the message ends the session):
// it goes out once, and the record says so although the loop is gone, so
// the next run does not post it again (which, without a key, shows it
// twice).
func TestK15ANoteWithoutAKeyPostedAsTheLoopClosesIsRecorded(t *testing.T) {
	fp := newPhoneFake(true)
	fp.features = []string{"activity", "dismiss", "attachments"}
	t.Cleanup(fp.srv.Close)
	held := make(chan struct{})
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(held) }) })
	front := k15Front(t, fp.srv.URL, "attachment_refused", held, nil)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = front })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	r10Says(fp,
		r10Msg("here is the screenshot of the error", map[string]any{"id": "att-9", "kind": "image", "content_type": "image/png", "filename": "IMG_9.png", "size": 3, "url": "/api/v1/attachments/att-9"}),
		r10Msg("/quit"))
	select {
	case <-g.rt.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("setup: the loop did not close")
	}
	released.Do(func() { close(held) })
	if code := g.exit(10 * time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := len(fp.postsWhere(k1Kind("attachment_refused"))); n != 1 {
		t.Fatalf("setup: the note was posted %d times", n)
	}
	if owed := owedRefusals(g.log()); len(owed) != 0 {
		t.Fatalf("the note went out after the loop closed and the record still owes it (%v): the next run posts it again", owed)
	}
	g.resume(Options{Interactive: true})
	r14Drained(t, g)
	g.interrupt()
	if n := len(fp.postsWhere(k1Kind("attachment_refused"))); n != 1 {
		t.Fatalf("the note was posted %d times", n)
	}
}

// A session last run without the ledger is resumed while the read of what
// was sent while it was stopped fails for good (as in
// TestSR3PhoneAMessageGivenUpAtAResumeIsNotTakenWithItsResend): the poll
// goes on without it, and the thread is to be told. The service does not
// take that note while the run lasts. It was one post at the mirror's
// start, lost with the run; it is owed on the record, and the next run
// posts it, once.
func TestK15ANoteThatWhatWasSentWasNotReadIsOwedUntilItGoesOut(t *testing.T) {
	setFor(t, &phoneRetryStep, 10*time.Millisecond)
	setFor(t, &phoneCloseWait, 300*time.Millisecond)
	cfg, project, path, fp := stoppedRelaySession(t)
	bphoneOlder(t, path)
	fp.userSays("the checklist is A, B, C", nil)
	var readFailed, refusing atomic.Bool
	refusing.Store(true)
	var tries atomic.Int32
	notes := k15Front(t, fp.srv.URL, "unread", nil, func() bool { return refusing.Load() && tries.Add(1) > 0 })
	u, err := url.Parse(notes)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") && readFailed.CompareAndSwap(false, true) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"not now"}}`))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg.Finalechat.BaseURL = srv.URL
	told := func() int {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		n := 0
		for _, p := range fp.posts {
			if meta, _ := p["meta"].(map[string]any); meta["kind"] == "unread" {
				n++
			}
		}
		return n
	}
	resume := func() (*Runtime, func()) {
		return bphoneRun(t, func() *Runtime {
			useFakeRelays(t, newFakeSeat("Done."))
			rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
			if err != nil {
				t.Fatal(err)
			}
			return rt
		})
	}
	rt, stop := resume()
	waitFor(t, "the note tried", func() bool { return tries.Load() > 0 })
	rt.sync(func() {})
	stop()
	if n := told(); n != 0 {
		t.Fatalf("setup: the note went out (%d)", n)
	}
	refusing.Store(false)
	rt2, stop2 := resume()
	waitFor(t, "the note posted by the next run", func() bool { return told() > 0 })
	rt2.sync(func() {})
	stop2()
	if n := told(); n != 1 {
		t.Fatalf("the thread was told %d times", n)
	}
}
