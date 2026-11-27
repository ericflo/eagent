package harness

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/integration"
)

// While a session is stopped the person sends a message from the phone.
// The session is then resumed from the terminal and stopped again at once
// (/quit). Its mirror starts, posts its first message and starts the poll,
// whose first read of the thread has not come back when the closing note
// has gone out and the mirror is cancelled. The message that waited was
// never read (not taken, not seen to be left), so the run writes nothing
// for the reply watcher, and the closing note hides the thread from the
// sweep. The watcher should resume the session with it.
func TestSR2PhoneAMessageThatWaitedIsLeftWhenTheRunEndsBeforeItsFirstRead(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	g := phoneStart(t, fp, "first ask", false, phoneStopAndClose("Nothing to do."), nil)
	if code := g.exit(20 * time.Second); code != 0 {
		t.Fatalf("first run exit %d", code)
	}
	ids := r10Says(fp, r10Msg("one more thing: bump the version"))
	closing := make(chan struct{})
	var once sync.Once
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") && r.URL.Query().Get("wait") != "" {
			// The poll's read, slower than the closing note.
			select {
			case <-closing:
			case <-time.After(10 * time.Second):
			}
			<-r.Context().Done()
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if bytes.Contains(raw, []byte(`"session_end"`)) {
				defer once.Do(func() { close(closing) })
			}
		}
		fp.handle(w, r)
	}))
	t.Cleanup(front.Close)
	g.cfg.Finalechat.BaseURL = front.URL
	g.resume(Options{Interactive: true, Prompt: "/quit"})
	if code := g.exit(30 * time.Second); code != 0 {
		t.Fatalf("resumed run exit %d", code)
	}
	select {
	case <-closing:
	default:
		t.Fatal("the closing note never went out")
	}
	if integration.ReadPhoneLedger(g.log()).Taken[ids[0]] {
		t.Fatal("the message was taken; the scenario did not happen")
	}
	t.Setenv("FINALECHAT_URL", fp.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	host := &k1LeftHost{next: make(chan integration.PhoneMessage, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	stop := integration.StartResumeWatcher(ctx, g.project, host, t.Logf)
	defer stop()
	defer cancel()
	select {
	case m := <-host.next:
		if m.ID != ids[0] {
			t.Fatalf("resumed with %#v, want the message that waited", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the message that waited was never resumed: the run wrote nothing for the reply watcher (no kept file) and its closing note hides the thread from the sweep")
	}
}
