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

// Finding 14 (twelfth review): a file whose download fails for now (a
// 502) is not refused: nothing is posted and the session is not resumed
// without it. Once the store answers again the session comes back with the
// reply and its file, nobody told it was not passed on. The watcher used
// to post the refusal and resume without the file at once.
func TestR12AFileThatCannotBeFetchedForNowIsNotRefused(t *testing.T) {
	_, _, _, fr, host, w := r9Stopped(t, "see the screenshot", r9Shot())
	fr.files.Store(http.StatusBadGateway)
	w.sweep(context.Background())
	if host.calls() != 0 || len(fr.posted()) != 0 {
		t.Fatalf("while the store fails for now: resumed %#v, posted %q", host.resumed, fr.posted())
	}
	fr.files.Store(0)
	w.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || len(host.resumed[0].Attachments) != 1 || len(fr.posted()) != 0 {
		t.Fatalf("once the store answers: resumed %#v, posted %q", host.resumed, fr.posted())
	}
}

// Finding 14 (twelfth review), its other case: a file too large to take
// and a second that cannot be fetched for now. Nothing is posted until
// every other file came, so the reply is still the thread's latest after
// a restart of the serve, and the new watcher's sweep resumes the session
// with it and the second file, the sender told once about the first. The
// watcher used to post the first notice at once, then lose the reply to
// the restart.
func TestR12ATooLargeFileIsToldOfOnlyOnceTheOthersCame(t *testing.T) {
	project, sid, f, fr, host, w := r11Restore(t, r10File("att-2", "shot.png"))
	fr.files.Store(http.StatusBadGateway)
	w.sweep(context.Background())
	if host.calls() != 0 || len(fr.posted()) != 0 || r10Preview(f, sid) != "user" {
		t.Fatalf("while shot.png cannot be fetched: resumed %#v, posted %q, latest from %q", host.resumed, fr.posted(), r10Preview(f, sid))
	}
	fr.files.Store(0)
	again := f.watcher(project, host, t) // the next serve's
	again.client = &finalechat.Client{BaseURL: fr.srv.URL, Token: "fc_fixture"}
	again.sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || len(host.resumed[0].Attachments) != 1 || host.resumed[0].Attachments[0].Name != "shot.png" {
		t.Fatalf("after the restart: resumed %#v", host.resumed)
	}
	if n := r11Told(fr, "clip.mov"); n != 1 || len(fr.posted()) != 1 {
		t.Fatalf("%d notices about clip.mov, posted %q", n, fr.posted())
	}
}

// Finding 14 (twelfth review): a restore takes a reply's files as the
// watcher does. A file the store says it does not have (404) is left out,
// its sender told, and the session comes back with the words; it used to
// fail the restore as a download that may come later does.
func TestR12ARestoreLeavesOutAFileTheStoreDoesNotHave(t *testing.T) {
	project, sid, _, fr, _, _ := r9Stopped(t, "here it is", r9Shot())
	t.Setenv("FINALECHAT_URL", fr.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	fr.files.Store(http.StatusNotFound)
	msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
	if err != nil || !ok || msg.ID != "m1" || msg.Text != "here it is" || len(msg.Attachments) != 0 {
		t.Fatalf("msg %#v ok=%v err=%v", msg, ok, err)
	}
	if got := fr.posted(); len(got) != 1 || !strings.Contains(got[0], "shot.png") {
		t.Fatalf("posted %q, want one notice about shot.png", got)
	}
}
