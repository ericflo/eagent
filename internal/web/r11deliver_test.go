package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/store"
)

// r11Model is a model server for these tests: every call waits, unanswered,
// until its caller gives up or the test ends. It returns the configuration
// that runs eagent on it.
func r11Model(t *testing.T) config.Config {
	t.Helper()
	quit := make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-quit:
		}
		http.Error(w, "no answers in this test", http.StatusServiceUnavailable)
	}))
	t.Cleanup(model.Close)
	t.Cleanup(func() { close(quit) })
	t.Setenv("EAGENT_TEST_KEY", "x")
	cfg := config.Defaults()
	cfg.Model.BaseURL, cfg.Model.APIKeyEnv, cfg.Model.Protocol, cfg.Model.Model, cfg.Model.Fallback = model.URL, "EAGENT_TEST_KEY", llm.ProtocolChat, "m", nil
	off := false
	cfg.Finalechat.Enabled, cfg.Finalechat.Artifacts = &off, false
	return cfg
}

// r11Session is a session that was started and has stopped.
func r11Session(t *testing.T, project string) store.Info {
	t.Helper()
	sess := newSession(t, project)
	if _, err := sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	info, err := store.Resolve(store.Root(project), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// r11Ended is a run of the session that has been told to stop and has
// stopped: it says it is ending, and reads its inbox no more.
func r11Ended(t *testing.T, cfg config.Config, project string, info store.Info) (*harness.Runtime, *webUI) {
	t.Helper()
	ui := newWebUI(func(string, ...any) {})
	rt, err := harness.Resume(cfg, harness.Options{Project: project, Interactive: true}, ui, info.Path)
	if err != nil {
		t.Fatal(err)
	}
	stopped, stop := context.WithCancel(context.Background())
	stop()
	rt.Run(stopped)
	if !rt.Ending() {
		t.Fatal("a stopped run does not say it is ending")
	}
	return rt, ui
}

// othersHold waits until another goroutine holds mu. The caller holds what
// that goroutine needs next, so it cannot let go of mu meanwhile.
func othersHold(t *testing.T, mu *sync.Mutex) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); mu.TryLock(); runtime.Gosched() {
		mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("nobody took the lock")
		}
	}
}

// takeFree waits until the caller holds mu.
func takeFree(t *testing.T, mu *sync.Mutex) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !mu.TryLock(); runtime.Gosched() {
		if time.Now().After(deadline) {
			t.Fatal("the lock was never let go")
		}
	}
}

// Finding 15: words from the browser wait for a run here that is ending,
// and when the run the session is resumed with next is ending too (the
// words it was resumed with were /quit), they wait for that one as well,
// and resume the session once it has stopped. They used to go into the
// second run's inbox, which it reads no more, answered "delivered".
func TestR11WordsWaitForEachEndingRunInTurn(t *testing.T) {
	s, project := newTestServer(t)
	cfg := r11Model(t)
	info := r11Session(t, project)
	rt, ui := r11Ended(t, cfg, project, info)
	lock, _, err := store.Open(info.Path, time.Now()) // the first run still holds its lock
	if err != nil {
		t.Fatal(err)
	}
	first := &hosted{rt: rt, ui: ui, done: make(chan struct{})}
	type result struct {
		how string
		err error
	}
	got := make(chan result, 1)
	resumed := 0
	// The request comes while the first run is ending. s.mu is held, so
	// that it is seen to have the lock deliver takes and to let it go.
	s.mu.Lock()
	s.running[info.ID] = first
	go func() {
		how, err := s.deliver(info, harness.InboxMessage{Type: "message", Text: "one more thing", From: "web"}, func() error {
			resumed++
			return nil
		})
		got <- result{how, err}
	}()
	othersHold(t, &s.resuming)
	s.mu.Unlock()
	takeFree(t, &s.resuming) // it saw the run ending and waits for it
	// The first run stops, and another request resumes the session (here
	// the test does, holding the lock as deliver does) with a run that is
	// ending from its start; it has closed its store and not yet returned.
	_ = lock.Close()
	second := &hosted{rt: rt, ui: ui, done: make(chan struct{})}
	s.mu.Lock()
	s.running[info.ID] = second
	close(first.done)
	s.resuming.Unlock()
	othersHold(t, &s.resuming) // the request looks again, under the lock
	s.mu.Unlock()
	takeFree(t, &s.resuming)
	s.mu.Lock()
	delete(s.running, info.ID) // the second run has stopped too
	s.mu.Unlock()
	close(second.done)
	s.resuming.Unlock()
	if r := <-got; r.err != nil || r.how != "resumed" || resumed != 1 {
		left, _ := os.ReadDir(harness.InboxDir(info.Path))
		t.Fatalf("answered %+v, resumed %d times, %d file(s) left in the inbox of a run that reads it no more", r, resumed, len(left))
	}
	if left, _ := os.ReadDir(harness.InboxDir(info.Path)); len(left) != 0 {
		t.Fatalf("%d file(s) left in the inbox", len(left))
	}
}
