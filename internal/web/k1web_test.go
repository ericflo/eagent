package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// k1Parked waits until a goroutine is parked taking a mutex from within
// fn, and reports true; or until done is closed (fn went on without
// waiting), and reports false.
func k1Parked(t *testing.T, fn string, done <-chan struct{}) bool {
	t.Helper()
	buf := make([]byte, 1<<20)
	for deadline := time.Now().Add(5 * time.Second); ; runtime.Gosched() {
		select {
		case <-done:
			return false
		default:
		}
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "sync.(*Mutex).Lock") && strings.Contains(g, fn) {
				return true
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s neither waited nor returned", fn)
		}
	}
}

// k1Model runs the server's sessions on the test's model: every call waits
// unanswered until the test ends, and no real provider is asked.
func k1Model(t *testing.T, s *Server, project string) config.Config {
	t.Helper()
	cfg := r11Model(t)
	if _, err := config.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	if c, err := s.loadConfig("", ""); err != nil || c.Model.BaseURL != cfg.Model.BaseURL || c.Model.Fallback != nil {
		t.Fatalf("the server would not run sessions on the test's model: %+v %v", c.Model, err)
	}
	t.Cleanup(func() { s.Shutdown(5 * time.Second) })
	return cfg
}

// D5: a reply from the phone for a session the browser is resuming at the
// same moment waits for that resume, and is then left to the run it
// started (whose mirror reads the thread). It used to resume the session
// too, and one of the two failed on the session's lock.
func TestK1PhoneResumeWaitsForTheBrowsersResume(t *testing.T) {
	s, project := newTestServer(t)
	k1Model(t, s, project)
	info := r11Session(t, project)
	entered, gate := make(chan struct{}), make(chan struct{})
	browser := make(chan error, 1)
	go func() {
		_, err := s.deliver(info, harness.InboxMessage{Type: "message", Text: "from the browser", From: "web"}, func() error {
			close(entered)
			<-gate // the browser's resume is under way
			return s.host(info.Path, harness.Options{Project: project, Interactive: true, Prompt: "from the browser", PromptSource: "web"}, "", "", true)
		})
		browser <- err
	}()
	<-entered
	phone, phoneDone := make(chan error, 1), make(chan struct{})
	go func() {
		phone <- s.Resume(info.ID, integration.PhoneMessage{ID: "m1", Text: "from the phone"})
		close(phoneDone)
	}()
	k1Parked(t, "web.(*Server).Resume", phoneDone)
	close(gate)
	if err := <-browser; err != nil {
		t.Fatalf("the browser's words were refused (%v): the phone's resume went ahead while the browser's was under way (%v)", err, <-phone)
	}
	if err := <-phone; err == nil {
		t.Fatal("the phone's reply resumed the session the browser had just resumed")
	}
	if !s.isHosted(info.ID) {
		t.Fatal("the session is not running here")
	}
}

// k1Recorded counts the user's messages on record with these words.
func k1Recorded(t *testing.T, info store.Info, text string) int {
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

// k1Refused checks that words from the browser were refused while the
// server stops, and left nowhere: not hosted, not on record, not in the
// inbox (the page puts them back in the box).
func k1Refused(t *testing.T, s *Server, info store.Info, w *httptest.ResponseRecorder, words string) {
	t.Helper()
	if s.isHosted(info.ID) {
		t.Fatalf("answered %d %s, and a run of the session started after the server's shutdown", w.Code, w.Body)
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("answered %d %s while the server stops", w.Code, w.Body)
	}
	if n := k1Recorded(t, info, words); n != 0 {
		t.Fatalf("the refused words are on record %d times", n)
	}
	if left, _ := os.ReadDir(harness.InboxDir(info.Path)); len(left) != 0 {
		t.Fatalf("%d file(s) left in the inbox", len(left))
	}
}

// D4: a message from the browser for a stopped session once the server's
// shutdown has begun starts no run: the process exit would kill it. It is
// refused (503), and the page keeps the words.
func TestK1NoRunStartsOnceTheServersShutdownHasBegun(t *testing.T) {
	s, project := newTestServer(t)
	k1Model(t, s, project)
	info := r11Session(t, project)
	s.Shutdown(5 * time.Second)
	w := do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": "one more thing"}, nil)
	k1Refused(t, s, info, w, "one more thing")
	w = do(s, "POST", "/api/sessions/"+info.ID+"/answer", map[string]any{"text": "yes"}, nil)
	k1Refused(t, s, info, w, "yes")
}

// D4: a message from the browser that waits for a run here to end while
// the server's shutdown stops that run is not taken by resuming the
// session once it has stopped, which would start a run nothing stops.
func TestK1WordsWaitingOnARunTheShutdownStopsStartNoRun(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	info := r11Session(t, project)
	rt, ui := r11Ended(t, cfg, project, info)
	lock, _, err := store.Open(info.Path, time.Now()) // the run still holds its lock
	if err != nil {
		t.Fatal(err)
	}
	h := &hosted{rt: rt, ui: ui, cancel: func() {}, done: make(chan struct{})}
	s.mu.Lock()
	s.running[info.ID] = h
	s.mu.Unlock()
	got := make(chan *httptest.ResponseRecorder, 1)
	s.mu.Lock()
	go func() {
		got <- do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": "one more thing"}, nil)
	}()
	othersHold(t, &s.resuming)
	s.mu.Unlock()
	takeFree(t, &s.resuming) // it saw the run ending and waits for it
	s.resuming.Unlock()
	stopped := make(chan struct{})
	s.mu.Lock()
	go func() { s.Shutdown(5 * time.Second); close(stopped) }()
	k1Parked(t, "web.(*Server).Shutdown", stopped) // it has begun
	s.mu.Unlock()
	// The run stops, as s.run has it once Run has returned.
	_ = lock.Close()
	s.mu.Lock()
	delete(s.running, info.ID)
	s.mu.Unlock()
	close(h.done)
	<-stopped
	k1Refused(t, s, info, <-got, "one more thing")
}
