package harness

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/integration"
)

// 1a9b54f, the reader's case: a command reaches a session that is ending
// and is taken, not run; the phone cannot take the note saying it was not
// run before the mirror closes. The session's log says it all the same:
// the command is not taken with no word.
func TestK4ACommandTakenWhileEndingIsSaidEvenWhenThePhoneNoteCannotGo(t *testing.T) {
	setFor(t, &phoneCloseWait, 200*time.Millisecond)
	fp := newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	// The service cannot take that note (503 until the mirror gives up).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if bytes.Contains(raw, []byte("command_not_run")) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"not now"}}`))
				return
			}
		}
		fp.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	g := phoneStart(t, fp, "start work", true, func(string, []map[string]any) reply { return hold() }, func(c *config.Config) { c.Finalechat.BaseURL = srv.URL })
	waitFor(t, "the agent", func() bool { return len(g.seat.sentTexts()) == 1 })
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
	g.exit(10 * time.Second)
	if l := integration.ReadPhoneLedger(g.log()); !l.Taken[ids[1]] {
		return // left for the next run, as before 1a9b54f: nothing was lost
	}
	if got := fp.postsWhere(k1Kind("command_not_run")); len(got) > 0 {
		return
	}
	if !strings.Contains(strings.Join(g.ui.logged(), "\n"), `did not run "/kill"`) {
		t.Fatalf("the command was taken and nobody was told it was not run: %q", g.ui.logged())
	}
}
