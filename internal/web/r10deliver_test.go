package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/store"
)

// Finding 15: words from the browser for a session this server hosts that
// is ending (its run has begun to stop and reads its inbox no more) are not
// put in its inbox, where nothing would read them: the request waits for
// the run to stop and then resumes the session with them, as for a session
// nobody runs.
func TestR10WordsForAnEndingSessionResumeItOnceItHasStopped(t *testing.T) {
	s, project := newTestServer(t)
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "no calls in this test", http.StatusInternalServerError)
	}))
	t.Cleanup(model.Close)
	t.Setenv("EAGENT_TEST_KEY", "x")
	cfg := config.Defaults()
	cfg.Model.BaseURL, cfg.Model.APIKeyEnv, cfg.Model.Protocol, cfg.Model.Model, cfg.Model.Fallback = model.URL, "EAGENT_TEST_KEY", llm.ProtocolChat, "m", nil
	off := false
	cfg.Finalechat.Enabled, cfg.Finalechat.Artifacts = &off, false
	sess := newSession(t, project)
	if _, err := sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	info, err := store.Resolve(store.Root(project), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	ui := newWebUI(func(string, ...any) {})
	rt, err := harness.Resume(cfg, harness.Options{Project: project, Interactive: true}, ui, info.Path)
	if err != nil {
		t.Fatal(err)
	}
	// The run is told to stop. What the server sees of a run in its
	// shutdown is then held here: its entry, its lock, done still open.
	stopped, stop := context.WithCancel(context.Background())
	stop()
	rt.Run(stopped)
	if !rt.Ending() {
		t.Fatal("a stopped run does not say it is ending")
	}
	lock, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	h := &hosted{rt: rt, ui: ui, done: make(chan struct{})}
	s.mu.Lock()
	s.running[info.ID] = h
	s.mu.Unlock()
	finish := func() { // as s.run once Run has returned
		_ = lock.Close()
		s.mu.Lock()
		delete(s.running, info.ID)
		s.mu.Unlock()
		close(h.done)
	}
	type result struct {
		how string
		err error
	}
	got, entered := make(chan result, 1), make(chan struct{})
	resumed := 0
	go func() {
		close(entered)
		how, err := s.deliver(info, harness.InboxMessage{Type: "message", Text: "one more thing", From: "web"}, func() error {
			resumed++
			return nil
		})
		got <- result{how, err}
	}()
	<-entered
	select {
	case r := <-got:
		finish()
		t.Fatalf("answered %+v while the session was ending", r)
	case <-time.After(20 * time.Millisecond):
	}
	finish()
	if r := <-got; r.err != nil || r.how != "resumed" || resumed != 1 {
		t.Fatalf("once it had stopped: %+v, resumed %d times", r, resumed)
	}
	if left, _ := os.ReadDir(harness.InboxDir(info.Path)); len(left) != 0 {
		t.Fatalf("%d file(s) left in the inbox", len(left))
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d model calls", n)
	}
}
