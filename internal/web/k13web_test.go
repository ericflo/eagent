package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// sr6Phone is a FinaleChat that keeps one copy of a post per client key,
// as the service does, and every post that has none.
type sr6Phone struct {
	mu    sync.Mutex
	notes []map[string]any // session_end posts kept
	keys  map[string]bool
}

func (f *sr6Phone) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		key, _ := body["client_key"].(string)
		meta, _ := body["meta"].(map[string]any)
		if kind, _ := meta["kind"].(string); kind == "session_end" && (key == "" || !f.keys[key]) {
			f.notes = append(f.notes, body)
			if key != "" {
				f.keys[key] = true
			}
		}
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"message":{"id":"m"},"thread":{"id":"t"}}`))
		return
	}
	if strings.HasSuffix(r.URL.Path, "/me") {
		_, _ = w.Write([]byte(`{"features":["idempotency","activity"]}`))
		return
	}
	_, _ = w.Write([]byte(`{}`))
}

// The user pressed Stop while `eagent serve` was stopping; the Stop
// stopped the agents, and its note to the phone ("eagent stopped this
// session") was taken by the service, but the process exited before the
// stop came off the list (its 2 s for requests ran out while the note's
// answer was on its way, or a second Ctrl+C quit it). The next serve
// finishes the stop again and tells the phone again. The run's own
// closing note carries a client key (eagent:<id>:end:quit:<n>) that makes
// a repeat one post; TellPhoneStopped never learned the service's
// features, so its note had none, and the thread showed the closing note
// twice. (Adapted: stopAgents now tells the phone itself, under the lock.)
func TestSR6WebAStopToldTwiceDoublesThePhonesClosingNote(t *testing.T) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "on")
	f := &sr6Phone{keys: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", srv.URL)
	k5Kills(t)
	info := srAgentSession(t, project)
	s.writeHosted([]string{info.ID}, time.Now().UTC())
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
	// stopAgentsOf's steps up to the phone note, as this server ran them.
	s.mu.Lock()
	s.markStop(info.ID)
	s.mu.Unlock()
	if _, _, err := stopAgents(info.Path, s.sessionConfig()); err != nil {
		t.Fatal(err)
	}
	// The process exits here, before forgetHosted. The next serve starts.
	oldRestore := restoreSession
	restoreSession = func(*Server, string, integration.PhoneMessage) error { return nil }
	t.Cleanup(func() { restoreSession = oldRestore })
	New(project, nil).restoreHosted(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.notes) != 1 {
		t.Fatalf("the phone's thread shows %d closing notes for one stop, want 1: %v", len(f.notes), f.notes)
	}
}

// A mark an earlier start could not finish stays on the list (as
// TestK11AMarkedStopThatFailsStaysMarked pins). The user then resumes the
// session in this server, and presses Stop on it while this server is
// stopping; the server exits before the run lets go. markStop finds the
// session marked already and keeps the old mark's time, from before the
// resume, so the next start judges the user's Stop by it: the session has
// run since, the stop "no longer holds", and the session the user just
// stopped is brought back, its agent left running.
func TestSR6WebAStopPressedOnAMarkedSessionKeepsTheOldMarksTime(t *testing.T) {
	s, project := newTestServer(t)
	killed := k5Kills(t)
	info := srAgentSession(t, project)
	old := time.Now().Add(-time.Hour).UTC()
	s.mu.Lock()
	s.writeHostedList([]string{info.ID}, old, k12Marks(old, info.ID))
	s.mu.Unlock()
	// Resumed here since: the record shows the run, and this server hosts it.
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{Interactive: true})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	s.mu.Lock()
	s.running[info.ID] = fakeHosted()
	s.mu.Unlock()
	t.Cleanup(func() { s.mu.Lock(); delete(s.running, info.ID); s.mu.Unlock() })
	s.rememberHosted() // this server's shutdown begins
	// The user presses Stop now: stopAgentsOf marks the stop first...
	s.mu.Lock()
	s.markStop(info.ID)
	s.mu.Unlock()
	// ...and the process exits before the run lets go of the session.
	var restored []string
	oldRestore := restoreSession
	restoreSession = func(_ *Server, id string, _ integration.PhoneMessage) error {
		restored = append(restored, id)
		return nil
	}
	t.Cleanup(func() { restoreSession = oldRestore })
	oldPending := pendingReply
	pendingReply = func(context.Context, string, store.Info) (integration.PhoneMessage, bool, error) {
		return integration.PhoneMessage{}, false, nil
	}
	t.Cleanup(func() { pendingReply = oldPending })
	New(project, nil).restoreHosted(context.Background())
	if len(restored) != 0 || len(*killed) == 0 {
		t.Fatalf("the Stop pressed during the shutdown was judged by a mark made before the resume: brought back %q, killed %q; want it kept stopped and its agent stopped", restored, *killed)
	}
}

// A session a terminal runs (`eagent`), in its last steps: its run has made
// its last look at the inbox (finish) and still holds the session's lock
// while its agents' starts and sends wind down. The page's message goes to
// the inbox (the session is alive), and the run lets go without reading
// it. It used to be answered "delivered" and left there, and the user, who
// saw it nowhere, sent it again: the next run took both. Now the server
// watches the file it wrote; when the run lets go of the session with the
// file still there, the server resumes the session, whose run takes the
// file, once. (Adapted from the review's test: the answer now says the
// words wait, and the user does not need to send them again.)
func TestSR6WebWordsLeftInATerminalRunsInboxAreTakenTwice(t *testing.T) {
	s, project := newTestServer(t)
	k1Model(t, s, project)
	old := inboxWatchEvery
	inboxWatchEvery = time.Millisecond
	t.Cleanup(func() { inboxWatchEvery = old })
	info := r11Session(t, project)
	// The terminal's run, past its last look, still holding the lock.
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	words := map[string]any{"text": "add the retry test"}
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/message", words, nil); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"inbox"`) {
		_ = sess.Close()
		t.Fatalf("send: %d %s; want it answered as waiting, not delivered", w.Code, w.Body)
	}
	_ = sess.Close() // the terminal's run lets go
	var h *hosted
	for deadline := time.Now().Add(5 * time.Second); h == nil; runtime.Gosched() {
		s.mu.Lock()
		h = s.running[info.ID]
		s.mu.Unlock()
		if h == nil && time.Now().After(deadline) {
			t.Fatal("the session was never resumed for the words left in its inbox")
		}
	}
	// A no-op hand-over, taken on the run's loop after its first look.
	if _, err := h.rt.Take(harness.InboxMessage{Type: "message", From: "web"}); err != nil {
		t.Fatal(err)
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == "add the retry test" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the session took the user's words %d times, want once", n)
	}
}

// The same kept mark, the session resumed in this server and then ended
// by itself (finished, or stopped on a question: not "quit", so nothing
// takes the mark off) before this server stops. rememberHosted listed the
// marked session beside the ones it hosts, so the next start, finding the
// mark stale, brought back a session that was not running when the server
// stopped.
func TestSR6WebAStaleMarkBringsBackASessionThatWasNotRunning(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	old := time.Now().Add(-time.Hour).UTC()
	s.mu.Lock()
	s.writeHostedList([]string{info.ID}, old, k12Marks(old, info.ID))
	s.mu.Unlock()
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{Interactive: true}),
		event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: "awaiting-input"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = sess.Close()
	s.rememberHosted() // this server stops, hosting nothing
	var restored []string
	oldRestore := restoreSession
	restoreSession = func(_ *Server, id string, _ integration.PhoneMessage) error {
		restored = append(restored, id)
		return nil
	}
	t.Cleanup(func() { restoreSession = oldRestore })
	oldPending := pendingReply
	pendingReply = func(context.Context, string, store.Info) (integration.PhoneMessage, bool, error) {
		return integration.PhoneMessage{}, false, nil
	}
	t.Cleanup(func() { pendingReply = oldPending })
	New(project, nil).restoreHosted(context.Background())
	if len(restored) != 0 {
		t.Fatalf("brought back %q, which was not running when the server stopped", restored)
	}
}
