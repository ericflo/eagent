package web

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/integration"
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
