package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/store"
)

// Finding 15 (twelfth review): words from the browser that wait for a run
// here to end, while the server is stopping, are not taken once the run has
// stopped: the request's context has ended (the server's did), so they are
// answered 503 and the session is not resumed after Shutdown has returned,
// by a run nothing would stop. They used to resume it and answer
// "resumed" as the process exited.
func TestR12WordsWaitingOnAnEndingRunAreNotTakenOnceTheRequestEnds(t *testing.T) {
	s, project := newTestServer(t)
	cfg := r11Model(t)
	// A resume here runs on the test's model, never a real one.
	if _, err := config.SaveBundle(project, "r12", "", cfg); err != nil {
		t.Fatal(err)
	}
	s.Bundle = "r12"
	t.Cleanup(func() { s.Shutdown(5 * time.Second) })
	info := r11Session(t, project)
	rt, ui := r11Ended(t, cfg, project, info)
	lock, _, err := store.Open(info.Path, time.Now()) // the run still holds its lock
	if err != nil {
		t.Fatal(err)
	}
	h := &hosted{rt: rt, ui: ui, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan *httptest.ResponseRecorder, 1)
	// The request comes while the run is ending; s.mu is held so that it
	// is seen to take the lock deliver takes and to let it go.
	s.mu.Lock()
	s.running[info.ID] = h
	go func() {
		got <- do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": "one more thing"}, func(r *http.Request) { *r = *r.WithContext(ctx) })
	}()
	othersHold(t, &s.resuming)
	s.mu.Unlock()
	takeFree(t, &s.resuming) // it saw the run ending and waits for it
	s.resuming.Unlock()
	// The server stops: its context, which the requests share, ends; then
	// Shutdown stops the run (as s.run does once Run has returned).
	cancel()
	_ = lock.Close()
	s.mu.Lock()
	delete(s.running, info.ID)
	s.mu.Unlock()
	close(h.done)
	w := <-got
	if w.Code != http.StatusServiceUnavailable || s.isHosted(info.ID) {
		t.Fatalf("answered %d %s, hosted again after the server stopped: %v", w.Code, w.Body, s.isHosted(info.ID))
	}
	if left, _ := os.ReadDir(harness.InboxDir(info.Path)); len(left) != 0 {
		t.Fatalf("%d file(s) left in the inbox", len(left))
	}
}
