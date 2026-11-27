package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/store"
)

// The user resumed a session from the page with a saved configuration
// picked ("nophone", which turns the phone mirror off; the project's own
// file has it on), so the run never mirrored. The server's shutdown begins
// and the user presses Stop: stopAgentsOf tells the phone through
// TellPhoneStopped, but with the server's own configuration
// (sessionConfig), not the one the session ran on, so a thread the
// session never had is written to (TestSR7WebAStopDuringTheShutdownOfAPhoneSessionTellsThePhoneNothing
// is the other way).
func TestSR7WebAStopDuringTheShutdownTellsThePhoneByTheConfigurationTheSessionRan(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	// The project's file: the phone on (the default).
	cfg := r11Model(t)
	cfg.Finalechat.Enabled = nil
	if _, err := config.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(5 * time.Second) })
	if c, err := s.loadConfig("", ""); err != nil || !c.Finalechat.Wanted() {
		t.Fatalf("setup: the server's configuration does not want the phone: %v", err)
	}
	// A saved configuration "nophone" that turns it off.
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "nophone"), []byte(`{"finalechat":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ran, err := s.loadConfig("nophone", ""); err != nil || ran.Finalechat.Wanted() {
		t.Fatalf("setup: the picked configuration wants the phone: %v", err)
	}
	var told []config.Config
	old := tellPhoneStopped
	tellPhoneStopped = func(cfg config.Config, _ string, _ func(event.Event)) { told = append(told, cfg) }
	t.Cleanup(func() { tellPhoneStopped = old })
	info := r11Session(t, project)
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/resume", map[string]any{"config": "nophone"}, nil); w.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", w.Code, w.Body)
	}
	s.mu.Lock()
	h := s.running[info.ID]
	s.mu.Unlock()
	if h == nil {
		t.Fatal("setup: the session is not hosted")
	}
	// The server's shutdown begins and ends the run; the user presses Stop.
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
	s.mu.Lock()
	s.writeHosted([]string{info.ID}, time.Now().UTC())
	s.mu.Unlock()
	h.cancel()
	<-h.done
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	if len(told) != 1 {
		t.Fatalf("setup: the phone was told %d times", len(told))
	}
	if told[0].Finalechat.Wanted() {
		t.Fatalf("the session ran with the phone off (the page's pick, config nophone); the stop told the phone by the server's own configuration, which has it on, so a thread the session never had is written to (header, closing note)")
	}
}

// The other way: the project's file has the phone off and the user resumed
// the session from the page with "phone" picked, so the run mirrored to
// the phone. The shutdown ends the run (its closing note: "eagent was
// interrupted. Resume the session to continue.") and the user presses
// Stop, which the answer calls done; the phone is told nothing of it (no
// "eagent stopped this session", no header or receipts), because the stop
// went by the server's own configuration, which has the phone off.
func TestSR7WebAStopDuringTheShutdownOfAPhoneSessionTellsThePhoneNothing(t *testing.T) {
	s, project := newTestServer(t)
	k1Model(t, s, project) // the project's file: the phone off
	t.Setenv("EAGENT_FINALECHAT", "")
	f := &sr6Phone{keys: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", srv.URL)
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "phone"), []byte(`{"finalechat":{"enabled":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ran, err := s.loadConfig("phone", ""); err != nil || !ran.Finalechat.Wanted() {
		t.Fatalf("setup: the picked configuration does not want the phone: %v", err)
	}
	info := r11Session(t, project)
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/resume", map[string]any{"config": "phone"}, nil); w.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", w.Code, w.Body)
	}
	s.mu.Lock()
	h := s.running[info.ID]
	s.mu.Unlock()
	if h == nil {
		t.Fatal("setup: the session is not hosted")
	}
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
	s.mu.Lock()
	s.writeHosted([]string{info.ID}, time.Now().UTC())
	s.mu.Unlock()
	h.cancel()
	<-h.done
	reasons := func() []string {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out []string
		for _, n := range f.notes {
			meta, _ := n["meta"].(map[string]any)
			r, _ := meta["reason"].(string)
			out = append(out, r)
		}
		return out
	}
	if got := reasons(); len(got) != 1 || got[0] != "interrupted" {
		t.Fatalf("setup: the run's closing notes: %q", got)
	}
	w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Stopped") {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	if got := reasons(); !slices.Contains(got, "quit") {
		t.Fatalf("the user pressed Stop and was answered %s, but the phone of the session, which mirrored to it, was told nothing: its last word is %q", strings.TrimSpace(w.Body.String()), got)
	}
}

// k14Bundles writes the project's file with the phone on and a saved
// configuration "nophone" that turns it off (no phone is reachable: no
// token).
func k14Bundles(t *testing.T, project string) {
	t.Helper()
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_TOKEN", "")
	cfg := r11Model(t)
	cfg.Finalechat.Enabled = nil
	if _, err := config.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "nophone"), []byte(`{"finalechat":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := New(project, nil).loadConfig("", ""); err != nil || !c.Finalechat.Wanted() {
		t.Fatalf("setup: a server's own configuration does not want the phone: %v", err)
	}
}

// k14Told replaces tellPhoneStopped with one that keeps the configurations
// it is told by.
func k14Told(t *testing.T) *[]config.Config {
	t.Helper()
	var told []config.Config
	old := tellPhoneStopped
	tellPhoneStopped = func(cfg config.Config, _ string, _ func(event.Event)) { told = append(told, cfg) }
	t.Cleanup(func() { tellPhoneStopped = old })
	return &told
}

// A stop the user asked during a shutdown whose server exits before
// finishing it is finished by the next server, which tells the phone by
// the configuration the session ran on: the mark names it (the page's
// pick, "nophone"), not the next server's own (the project's file, phone
// on).
func TestK14AMarkTellsThePhoneByTheConfigurationTheSessionRan(t *testing.T) {
	s, project := newTestServer(t)
	k14Bundles(t, project)
	t.Cleanup(func() { s.Shutdown(5 * time.Second) })
	told := k14Told(t)
	info := r11Session(t, project)
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/resume", map[string]any{"config": "nophone"}, nil); w.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", w.Code, w.Body)
	}
	s.mu.Lock()
	h := s.running[info.ID]
	s.writeHosted([]string{info.ID}, time.Now().UTC())
	s.markStop(info.ID) // the user's Stop, before this server exits
	s.mu.Unlock()
	h.cancel()
	<-h.done
	next := New(project, nil)
	next.restoreHosted(context.Background())
	if len(*told) != 1 || (*told)[0].Finalechat.Wanted() {
		t.Fatalf("the next server finished the stop telling the phone %d time(s), the last by a configuration that wants the phone, the session's being nophone", len(*told))
	}
}

// A mark made by an eagent from before marks named the session's
// configuration is finished telling the phone by the configuration the
// session's record names, and the log says so.
func TestK14AnOldMarkTellsThePhoneByTheConfigurationTheRecordNames(t *testing.T) {
	_, project := newTestServer(t)
	k14Bundles(t, project)
	told := k14Told(t)
	sess := newSession(t, project)
	if _, err := sess.Append(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project, Config: "nophone"})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	at := time.Now().UTC()
	list := fmt.Sprintf(`{"sessions": [], "at": %q, "stop": [{"session": %q, "at": %q}]}`, at.Format(time.RFC3339Nano), sess.ID, at.Format(time.RFC3339Nano))
	if err := os.WriteFile(hostedFile(project), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	var logged []string
	next := New(project, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	next.restoreHosted(context.Background())
	if len(*told) != 1 || (*told)[0].Finalechat.Wanted() {
		t.Fatalf("the stop was finished telling the phone %d time(s), the last by a configuration that wants the phone; the record names nophone", len(*told))
	}
	if !slices.ContainsFunc(logged, func(l string) bool { return strings.Contains(l, "record names") }) {
		t.Fatalf("the log does not say the configuration came from the record: %q", logged)
	}
}

// sr7WatcherGone waits until no watchInbox goroutine is left, or the
// session is hosted here; it reports whether it is hosted.
func sr7WatcherGone(t *testing.T, s *Server, id string) bool {
	t.Helper()
	buf := make([]byte, 1<<20)
	for deadline := time.Now().Add(5 * time.Second); ; runtime.Gosched() {
		if s.isHosted(id) {
			return true
		}
		n := runtime.Stack(buf, true)
		if !strings.Contains(string(buf[:n]), "web.(*Server).watchInbox") {
			return s.isHosted(id)
		}
		if time.Now().After(deadline) {
			t.Fatal("the inbox watcher neither resumed the session nor returned")
		}
	}
}

// A session a terminal runs, in its last steps (past its last look at the
// inbox, the lock still held). The user writes to it from the page (the
// words wait, "queued"), then presses Stop (it goes to the inbox too,
// answered ok). The run lets go. On main both files stay in the inbox and
// the session stays stopped. Here the watcher resumed the session for the
// words, and the resumed run dropped the user's Stop as "left by an
// earlier run": the session the user stopped ran on.
func TestSR7WebAStopAfterQueuedWordsIsDroppedByTheWatchersResume(t *testing.T) {
	s, project := newTestServer(t)
	k1Model(t, s, project)
	old := inboxWatchEvery
	inboxWatchEvery = time.Millisecond
	t.Cleanup(func() { inboxWatchEvery = old })
	info := r11Session(t, project)
	sess, _, err := store.Open(info.Path, time.Now()) // the terminal's run, past its last look
	if err != nil {
		t.Fatal(err)
	}
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": "and add the retry test"}, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"queued"`) {
		_ = sess.Close()
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", map[string]any{}, nil); w.Code != http.StatusOK {
		_ = sess.Close()
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	_ = sess.Close() // the terminal's run lets go
	if !sr7WatcherGone(t, s, info.ID) {
		return // not resumed: the stop holds
	}
	s.mu.Lock()
	h := s.running[info.ID]
	s.mu.Unlock()
	if h == nil {
		return // resumed and already stopped
	}
	// A no-op hand-over, taken on the run's loop after its first look: a
	// run that took the stop is ending and takes nothing.
	took, err := h.rt.Take(harness.InboxMessage{Type: "message", From: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if took {
		t.Fatalf("the user pressed Stop (answered ok), and the server resumed the session for the words queued before it; its run dropped the stop as left by an earlier run and runs on (words on record %d time(s))", k1Recorded(t, info, "and add the retry test"))
	}
}

// Words for a session another process runs, sent once this server's
// shutdown has begun, are answered with what is true: they wait in the
// session's inbox until it next runs (this server resumes nothing now).
// The answer used to promise they would be taken "when it is resumed for
// them", which nobody would do.
func TestK14WordsQueuedDuringTheShutdownPromiseNoResume(t *testing.T) {
	s, project := newTestServer(t)
	old := inboxWatchEvery
	inboxWatchEvery = time.Millisecond
	t.Cleanup(func() { inboxWatchEvery = old })
	info := r11Session(t, project)
	sess, _, err := store.Open(info.Path, time.Now()) // a terminal runs it
	if err != nil {
		t.Fatal(err)
	}
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
	w := do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": "one more thing"}, nil)
	_ = sess.Close() // the terminal's run lets go
	if sr7WatcherGone(t, s, info.ID) {
		t.Fatal("the session was resumed during the shutdown")
	}
	s.resuming.Lock() // after the watcher's last step (it unlocks this)
	s.resuming.Unlock()
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"queued"`) || strings.Contains(w.Body.String(), "resumed") || !strings.Contains(w.Body.String(), "next runs") {
		t.Fatalf("answered %d %s", w.Code, w.Body)
	}
}

// sr7Held is a terminal's UI whose loop the test holds while it records
// the line "hold".
type sr7Held struct {
	*webUI
	in         chan string
	held, hold chan struct{}
	once       sync.Once
}

func (u *sr7Held) Input() <-chan string { return u.in }
func (u *sr7Held) Trace(ev event.Event) {
	var d event.UserMessageData
	if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == "hold" {
		u.once.Do(func() { close(u.held); <-u.hold })
	}
}

// A session a terminal runs (another process: not hosted here). The user
// presses Stop on the page and then writes to it (Stop goes to the inbox,
// answered ok; the words are "queued", watched). The run reads both in one
// look: the stop ends it, and the words after it, read by a run that is
// ending, go on its record "kept for the next run" and their file is
// removed. The watcher took the removed file as taken and returned: no run
// was started for the words, which stayed on the record unanswered. For a
// run hosted here the same words wait for it to stop and resume the
// session (finding 15, TestR11WordsWaitForEachEndingRunInTurn); the page
// was told the words wait "until it takes them ... or it is resumed for
// them".
func TestSR7WebWordsAnEndingTerminalRunTakesAreNeverAnswered(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	old := inboxWatchEvery
	inboxWatchEvery = time.Millisecond
	t.Cleanup(func() { inboxWatchEvery = old })
	info := r11Session(t, project)
	ui := &sr7Held{webUI: newWebUI(func(string, ...any) {}), in: make(chan string, 1), held: make(chan struct{}), hold: make(chan struct{})}
	rt, err := harness.Resume(cfg, harness.Options{Project: project, Interactive: true}, ui, info.Path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runDone := make(chan struct{})
	go func() { rt.Run(ctx); close(runDone) }()
	ui.in <- "hold" // typed in the terminal; its loop is held while it records it
	<-ui.held
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", map[string]any{}, nil); w.Code != http.StatusOK {
		close(ui.hold)
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	words := "one more thing: add the retry test"
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": words}, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"queued"`) {
		close(ui.hold)
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	close(ui.hold)
	<-runDone
	hosted := sr7WatcherGone(t, s, info.ID)
	left, _ := os.ReadDir(harness.InboxDir(info.Path))
	n := k1Recorded(t, info, words)
	if !hosted && !alive(info.Path) {
		t.Fatalf("the words written after the Stop are on the record %d time(s) (inbox holds %d file(s)), and no run was started for them: the watcher took their removed file as taken; the page was told they wait until the session takes them or is resumed for them", n, len(left))
	}
}
