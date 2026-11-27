package web

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/store"
)

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
