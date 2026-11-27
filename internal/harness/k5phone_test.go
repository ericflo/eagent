package harness

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/integration"
)

// srPhoneKept is the project's kept file (integration.keptFile).
func srPhoneKept(project string) string {
	return filepath.Join(project, ".agents", "eagent", "finalechat-state", "resume-watch-kept.json")
}

// A reply the person sends while the session is in its last steps (after
// the loop has stopped taking work, before the mirror is cancelled: here,
// while the closing note is on its way) is read by the poll, whose hand-off
// to the loop is dropped. It is neither taken in nor left for the next run
// (p.left stays false, the poll's context is cancelled), so LeftForNextRun
// writes nothing; the closing note then hides it from the reply watcher's
// sweep. Sibling of 7d55b9d (B14's other half), which covers a message the
// loop sees while ending.
func TestSRPhoneAReplyReadAfterTheLoopClosedIsLeftForTheNextRun(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	var mu sync.Mutex
	lateID := ""
	handled := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") {
			mu.Lock()
			id := lateID
			mu.Unlock()
			if id != "" && r.URL.Query().Get("after") == id {
				once.Do(func() { close(handled) }) // the poll is done with the reply
			}
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if bytes.Contains(raw, []byte(`"session_end"`)) {
				// The person writes as the session closes.
				fp.userReply("one more thing: bump the version")
				mu.Lock()
				lateID = fp.lastMessageID()
				mu.Unlock()
				select {
				case <-handled:
				case <-time.After(5 * time.Second):
				case <-r.Context().Done():
					return
				}
			}
		}
		fp.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	g := phoneStart(t, fp, "first ask", false, phoneStopAndClose("Nothing to do."), func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
	if code := g.exit(30 * time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	select {
	case <-handled:
	default:
		t.Fatal("the poll never read the reply sent while the session closed")
	}
	mu.Lock()
	id := lateID
	mu.Unlock()
	taken := integration.ReadPhoneLedger(g.log()).Taken[id]
	_, keptErr := os.Stat(srPhoneKept(g.project))
	// What the person sees: the project's reply watcher, started now, is
	// to resume the session with the reply (as in TestK1AMessageLeftBehind…).
	t.Setenv("FINALECHAT_URL", fp.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	host := &k1LeftHost{next: make(chan integration.PhoneMessage, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	stop := integration.StartResumeWatcher(ctx, g.project, host, t.Logf)
	defer stop()
	defer cancel()
	select {
	case m := <-host.next:
		if m.ID != id {
			t.Fatalf("resumed with %#v, want the reply %s", m, id)
		}
	case <-time.After(3 * time.Second):
		if !taken {
			t.Fatalf("the reply %s sent while the session closed was neither taken in (ledger) nor left for the reply watcher (kept file: %v); nothing resumes the session with it", id, keptErr)
		}
	}
}
