package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// The configuration page lists the models each hosted session is using
// while that session's loop goes on changing routes. Reading them must not
// touch the loop's own state: a map read beside the loop's write crashes
// the whole server and every session it hosts.
func TestConfigPageReadsARunningSessionsModelsWithoutRacingItsLoop(t *testing.T) {
	s, project := newTestServer(t)
	sess := newSession(t, project)
	if _, err := sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project, Models: map[string]string{"eagent": "first"}})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	info, err := store.Resolve(store.Root(project), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := info.Path
	// The session is never run, so nothing calls the provider.
	t.Setenv("TOGETHER_API_KEY", "unused")
	cfg, err := s.loadConfig("", "")
	if err != nil {
		t.Fatal(err)
	}
	ui := newWebUI(func(string, ...any) {})
	rt, err := harness.Resume(cfg, harness.Options{Project: project}, ui, path)
	if err != nil {
		t.Fatal(err)
	}
	h := fakeHosted()
	h.rt, h.ui = rt, ui
	s.mu.Lock()
	s.running[sess.ID] = h
	s.mu.Unlock()
	// The loop's side: route changes folded into the live state, as
	// Runtime.append does on the loop goroutine.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			ev := event.New(event.Route, event.ActorHarness, event.RouteData{Actor: "eagent", Model: fmt.Sprintf("m%d", i%7)})
			ev.Seq = int64(1000 + i)
			rt.State().Apply(ev)
		}
	}()
	for i := 0; i < 20; i++ {
		if w := do(s, "GET", "/api/config", nil, nil); w.Code != 200 {
			t.Fatalf("config = %d %s", w.Code, w.Body)
		}
	}
	close(stop)
	<-done
	var v struct {
		Running []runningView `json:"running"`
	}
	if w := do(s, "GET", "/api/config", nil, nil); json.Unmarshal(w.Body.Bytes(), &v) != nil || len(v.Running) != 1 || v.Running[0].Models["eagent"] == "" {
		t.Fatalf("running = %+v", v.Running)
	}
}

// A session the server was hosting that has no phone thread (FinaleChat
// says the thread does not exist) cannot have a reply waiting in it, so it
// comes back like any other, instead of being left stopped with the list
// that named it already gone.
func TestRestartBringsBackASessionWhoseThreadDoesNotExist(t *testing.T) {
	s, project := newTestServer(t)
	sess := newSession(t, project)
	_ = sess.Close()
	s.running[sess.ID] = fakeHosted()
	s.rememberHosted()
	var got []string
	oldRestore, oldReply := restoreSession, pendingReply
	t.Cleanup(func() { restoreSession, pendingReply = oldRestore, oldReply })
	restoreSession = func(s *Server, id string, reply integration.PhoneMessage) error {
		got = append(got, id+"|"+reply.Text)
		return nil
	}
	pendingReply = func(context.Context, string, store.Info) (integration.PhoneMessage, bool, error) {
		return integration.PhoneMessage{}, false, &finalechat.Error{Status: 404, Code: "not_found", Message: "Not found."}
	}
	New(project, nil).restoreHosted(context.Background())
	if len(got) != 1 || got[0] != sess.ID+"|" {
		t.Fatalf("brought back %v, want %s with no reply", got, sess.ID)
	}
}

// A 404 that is not FinaleChat's own (a proxy in front of it during a
// deploy) says nothing about the thread: that session is left to the reply
// watcher, as when the service cannot be reached.
func TestRestartLeavesASessionWhenA404IsNotFinaleChats(t *testing.T) {
	s, project := newTestServer(t)
	sess := newSession(t, project)
	_ = sess.Close()
	s.running[sess.ID] = fakeHosted()
	s.rememberHosted()
	var got []string
	oldRestore, oldReply := restoreSession, pendingReply
	t.Cleanup(func() { restoreSession, pendingReply = oldRestore, oldReply })
	restoreSession = func(s *Server, id string, reply integration.PhoneMessage) error {
		got = append(got, id)
		return nil
	}
	pendingReply = func(context.Context, string, store.Info) (integration.PhoneMessage, bool, error) {
		return integration.PhoneMessage{}, false, &finalechat.Error{Status: 404, Message: "404 page not found"}
	}
	New(project, nil).restoreHosted(context.Background())
	if len(got) != 0 {
		t.Fatalf("brought back %v without reading its thread", got)
	}
}

// TestBrowserClosedQuestionsAndTheToolbarFollowTheStream: a question card
// answered or dismissed while the page is open stops offering its options,
// and the toolbar (Stop or Resume, the model) follows the session's status
// as it streams in. Runs by hand, like TestBrowserOldSession.
func TestBrowserClosedQuestionsAndTheToolbarFollowTheStream(t *testing.T) {
	module := os.Getenv("EAGENT_PLAYWRIGHT_MODULE")
	if module == "" {
		t.Skip("set EAGENT_PLAYWRIGHT_MODULE to run the browser test")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	old := streamStatusEvery
	streamStatusEvery = 100 * time.Millisecond
	t.Cleanup(func() { streamStatusEvery = old })
	s, project := newTestServer(t)
	sess := newSession(t, project)
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project, Models: map[string]string{"eagent": "o/first"}}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{Index: 0, Reason: "new"}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "ship it?"}),
		event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q1", Text: "Push now?", Options: []string{"yes", "no"}}),
		event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q2", Text: "Tag it too?", Options: []string{"yes", "no"}}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	info, err := store.Resolve(store.Root(project), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := fakeHosted()
	h.ui = newWebUI(func(string, ...any) {})
	s.mu.Lock()
	s.running[sess.ID] = h
	s.mu.Unlock()
	srv := httptest.NewServer(s.loopbackOnly(s.Handler()))
	defer srv.Close()
	cmd := exec.Command("node", filepath.Join("testdata", "live-session-browser.mjs"), srv.URL, sess.ID)
	cmd.Env = append(os.Environ(), "EAGENT_PLAYWRIGHT_MODULE="+module)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	out := bufio.NewScanner(stdout)
	var said []string
	for out.Scan() {
		said = append(said, out.Text())
		if out.Text() == "ready" {
			break
		}
	}
	if len(said) == 0 || said[len(said)-1] != "ready" {
		_ = cmd.Wait()
		t.Fatalf("the page never showed the session: %s\n%s", strings.Join(said, "\n"), stderr.String())
	}
	// The session answers one question, dismisses the other, changes model
	// and stops being hosted here.
	again, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: "q1", Text: "yes", Source: "web"}),
		event.New(event.UserDismiss, event.ActorUser, event.UserDismissData{QuestionID: "q2", Source: "web"}),
		event.New(event.Route, event.ActorHarness, event.RouteData{Actor: "eagent", Model: "o/second"}),
	} {
		if _, err := again.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = again.Close()
	s.mu.Lock()
	delete(s.running, sess.ID)
	s.mu.Unlock()
	_, _ = io.WriteString(stdin, "go\n")
	for out.Scan() {
		said = append(said, out.Text())
	}
	err = cmd.Wait()
	if err != nil || said[len(said)-1] != "ok" {
		t.Fatalf("browser test failed: %v\n%s\n%s", err, strings.Join(said, "\n"), stderr.String())
	}
}
