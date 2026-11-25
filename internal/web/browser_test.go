package web

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// TestBrowserOldSession drives the real page in a browser over an old
// session: an old #/s/<id>/tasks bookmark opens the chat, its task, note,
// yield and process events read as rows without a toggle, its closing
// reason shows ("done, waiting for you"), and the boundary where the
// current eagent took an older session over shows in the chat.
//
// It needs Playwright, so it runs by hand:
//
//	EAGENT_PLAYWRIGHT_MODULE=/path/to/node_modules/playwright/index.mjs go test ./internal/web -run Browser
//
// EAGENT_BROWSER_SHOTS=<dir> also saves screenshots. With
// EAGENT_BROWSER_PROJECT=<project> it serves that project's sessions
// instead of the fixture and only takes the screenshots (of each session
// named in EAGENT_BROWSER_SESSIONS, space separated).
func TestBrowserOldSession(t *testing.T) {
	module := os.Getenv("EAGENT_PLAYWRIGHT_MODULE")
	if module == "" {
		t.Skip("set EAGENT_PLAYWRIGHT_MODULE to run the browser test")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	s, project := newTestServer(t)
	args := []string{filepath.Join("testdata", "old-session-browser.mjs")}
	if real := os.Getenv("EAGENT_BROWSER_PROJECT"); real != "" {
		s = New(real, nil)
		args = append(args, "look", os.Getenv("EAGENT_BROWSER_SESSIONS"))
	} else {
		done, upgraded := oldSessionFixtures(t, project)
		// The finished one is shown as running here, so the live line says
		// how it stopped.
		h := fakeHosted()
		h.ui = newWebUI(func(string, ...any) {})
		s.mu.Lock()
		s.running[done] = h
		s.mu.Unlock()
		args = append(args, "check", done+" "+upgraded)
	}
	srv := httptest.NewServer(s.loopbackOnly(s.Handler()))
	defer srv.Close()
	cmd := exec.Command("node", append(args[:1], append([]string{srv.URL}, args[1:]...)...)...)
	cmd.Env = append(os.Environ(), "EAGENT_PLAYWRIGHT_MODULE="+module)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("browser test failed: %v", err)
	}
}

// TestBrowserNewSessionWithThis: the configuration page's "New session
// with this" starts on the project's own configuration (use_project), not
// the server's --preset. Runs by hand, like TestBrowserOldSession.
func TestBrowserNewSessionWithThis(t *testing.T) {
	module := os.Getenv("EAGENT_PLAYWRIGHT_MODULE")
	if module == "" {
		t.Skip("set EAGENT_PLAYWRIGHT_MODULE to run the browser test")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	s, _ := newTestServer(t)
	srv := httptest.NewServer(s.loopbackOnly(s.Handler()))
	defer srv.Close()
	cmd := exec.Command("node", filepath.Join("testdata", "old-session-browser.mjs"), srv.URL, "config", "")
	cmd.Env = append(os.Environ(), "EAGENT_PLAYWRIGHT_MODULE="+module)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("browser test failed: %v", err)
	}
}

// oldSessionFixtures writes two sessions from before eagent was one loop:
// one that finished, and one the current eagent took over.
func oldSessionFixtures(t *testing.T, project string) (done, upgraded string) {
	t.Helper()
	legacy := func(at time.Time, extra ...event.Event) string {
		sess, err := store.Create(store.Root(project), at)
		if err != nil {
			t.Fatal(err)
		}
		evs := []event.Event{
			event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Cwd: project, Models: map[string]string{event.LegacyOrchestrator: "o/orch", event.LegacyTask: "o/task", event.LegacyNarrator: "o/narr"}}),
			event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{Index: 0, Reason: "new"}),
			event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "review the parser"}),
			event.New(event.LegacyNote, event.LegacyOrchestrator, map[string]any{"text": "starting with the tokenizer"}),
			event.New(event.LegacyTaskCreate, event.LegacyOrchestrator, map[string]any{"id": "t1", "title": "Review the tokenizer", "description": "look for off-by-one bugs", "kind": "work"}),
			{Type: event.LegacyTaskStart, Actor: event.ActorHarness, Task: "t1", Data: []byte(`{"id":"t1"}`)},
			{Type: event.LegacyProcStart, Actor: event.LegacyTask, Task: "t1", Data: []byte(`{"handle":"p1","command":"go test ./parser","timeout_s":600}`)},
			{Type: event.LegacyProcExit, Actor: event.LegacyTask, Task: "t1", Data: []byte(`{"handle":"p1","exit_code":0,"reason":"exited","duration_ms":900}`)},
			{Type: event.LegacyTaskEnd, Actor: event.ActorHarness, Task: "t1", Data: []byte(`{"id":"t1","status":"completed","summary":"found one off-by-one bug in peek"}`)},
			event.New(event.LegacyNarratorTrim, event.LegacyNarrator, map[string]any{"from_seq": 3, "recap": "told the user the tokenizer review began"}),
			event.New(event.Message, event.LegacyNarrator, map[string]any{"text": "The tokenizer had one bug, in peek; it is fixed."}),
			event.New(event.LegacyYield, event.LegacyOrchestrator, event.LegacyYieldData{Done: true, Reason: "reviewed the tokenizer and fixed peek"}),
		}
		for _, ev := range append(evs, extra...) {
			if _, err := sess.Append(ev); err != nil {
				t.Fatal(err)
			}
		}
		_ = sess.Close()
		return sess.ID
	}
	now := time.Now()
	done = legacy(now.Add(-time.Hour))
	upgraded = legacy(now.Add(-30*time.Minute),
		event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "upgrade"}),
		event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{Index: 1, Reason: "upgrade"}),
		event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "briefing"}),
		event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "now the lexer"}),
		event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "codex", State: "working"}),
	)
	return done, upgraded
}
