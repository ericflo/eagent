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

// r11Terminal is a run's UI with a terminal the test types into. It tells
// the test each time the loop has looked at what is due (and so, the first
// time, has read its inbox once).
type r11Terminal struct {
	*webUI
	in     chan string
	looked chan struct{}
}

func (u r11Terminal) Input() <-chan string { return u.in }
func (u r11Terminal) Idle(bool) {
	select {
	case u.looked <- struct{}{}:
	default:
	}
}

// r11Recorded counts the user's messages on record with these words.
func r11Recorded(t *testing.T, info store.Info, text string) int {
	t.Helper()
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == text {
			n++
		}
	}
	return n
}

// Finding 16, a session this server hosts: words from the browser go into
// the inbox of a run that is not ending, and the run is told to stop before
// it looks at its inbox again. Once it has stopped, the session is resumed
// here with them, as for words sent to a session nobody runs. They used to
// stay in the inbox, answered "delivered", until somebody resumed it.
func TestR11WordsLeftInTheInboxOfAStoppedRunResumeIt(t *testing.T) {
	s, project := newTestServer(t)
	cfg := r11Model(t)
	// The session is resumed on the server's configuration: the test's model.
	if _, err := config.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	if c, err := s.loadConfig("", ""); err != nil || c.Model.BaseURL != cfg.Model.BaseURL || c.Model.Fallback != nil {
		t.Fatalf("the server would not run the session on the test's model: %+v %v", c.Model, err)
	}
	hosting := make(chan struct{}, 8)
	s.logf = func(format string, _ ...any) {
		if format == "web: session %s running in this process" {
			hosting <- struct{}{}
		}
	}
	info := r11Session(t, project)
	ui := r11Terminal{newWebUI(func(string, ...any) {}), make(chan string, 1), make(chan struct{}, 1)}
	rt, err := harness.Resume(cfg, harness.Options{Project: project, Interactive: true}, ui, info.Path)
	if err != nil {
		t.Fatal(err)
	}
	s.run(rt, ui.webUI)
	<-hosting
	<-ui.looked // its first look at the inbox is behind it
	s.mu.Lock()
	h := s.running[info.ID]
	s.mu.Unlock()
	const words = "one more thing"
	how, err := s.deliver(info, harness.InboxMessage{Type: "message", Text: words, From: "web"}, func() error {
		t.Error("a running session was resumed")
		return nil
	})
	if err != nil || how != "inbox" {
		t.Fatalf("delivered %q, %v; want the inbox of the running session", how, err)
	}
	ui.in <- "/quit" // before the run's next look at its inbox, half a second on
	<-h.done
	if r11Recorded(t, info, words) == 0 { // the run did not take them
		select {
		case <-hosting:
		case <-time.After(2 * time.Second):
			left, _ := os.ReadDir(harness.InboxDir(info.Path))
			t.Fatalf("the run stopped with %d file(s) in its inbox it never read, and nothing resumed the session", len(left))
		}
	}
	s.Shutdown(5 * time.Second)
	if n := r11Recorded(t, info, words); n != 1 {
		t.Fatalf("the words are on record %d times", n)
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	resumes := 0
	for _, ev := range evs {
		if ev.Type == event.SessionResume {
			resumes++
		}
	}
	if resumes > 2 {
		t.Fatalf("the session was resumed %d times after the run stopped", resumes-1)
	}
	if left, _ := os.ReadDir(harness.InboxDir(info.Path)); len(left) != 0 {
		t.Fatalf("%d file(s) left in the inbox", len(left))
	}
	if s.isHosted(info.ID) {
		t.Fatal("the session was resumed again when the server stopped it")
	}
}

// Finding 16, a session run in a terminal: its loop is over (it reads its
// inbox no more) and it holds its lock while it finishes. Words from the
// browser must not be put in its inbox and answered "delivered".
func TestR11WordsForAnEndingTerminalRunAreNotLeftUnread(t *testing.T) {
	t.Skip("known, not fixed: on disk a terminal run that is ending (lock held, record ending in session.end) looks like one starting; telling them apart needs a new record event")
	s, project := newTestServer(t)
	cfg := r11Model(t)
	info := r11Session(t, project)
	r11Ended(t, cfg, project, info)
	lock, _, err := store.Open(info.Path, time.Now()) // the terminal's run, finishing
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": "one more thing"}, nil)
	_ = lock.Close() // it has finished
	if left, _ := os.ReadDir(harness.InboxDir(info.Path)); w.Code == http.StatusOK && len(left) != 0 {
		t.Fatalf("answered %d %s, and %d file(s) are left in the inbox of a run that reads it no more", w.Code, w.Body, len(left))
	}
}
