package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
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

// A session a terminal runs (`eagent`), in its last steps: its run has made
// its last look at the inbox (finish) and still holds the session's lock
// while its agents' starts and sends wind down. The page's message goes to
// the inbox (the session is alive), and the run lets go without reading
// it. It used to be answered "delivered" and left there, and the user, who
// saw it nowhere, sent it again: the next run took both. Now the answer
// says the words wait in the session's inbox, and the page shows them
// waiting; the session's next run (the page's Resume) takes them, once.
// (Adapted from the review's test, and again when the server stopped
// resuming a session another process ran for words it left there.)
func TestSR6WebWordsLeftInATerminalRunsInboxAreTakenTwice(t *testing.T) {
	s, project := newTestServer(t)
	k1Model(t, s, project)
	info := r11Session(t, project)
	// The terminal's run, past its last look, still holding the lock.
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	words := map[string]any{"text": "add the retry test"}
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/message", words, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"delivered":"queued"`) {
		_ = sess.Close()
		t.Fatalf("send: %d %s; want it answered as waiting, not delivered", w.Code, w.Body)
	}
	_ = sess.Close() // the terminal's run lets go
	if left, _ := os.ReadDir(harness.InboxDir(info.Path)); s.isHosted(info.ID) || len(left) != 1 {
		t.Fatalf("hosted %v, inbox %d file(s); want the words waiting in the inbox", s.isHosted(info.ID), len(left))
	}
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/resume", map[string]any{}, nil); w.Code != http.StatusOK {
		t.Fatalf("the page's Resume: %d %s", w.Code, w.Body)
	}
	s.mu.Lock()
	h := s.running[info.ID]
	s.mu.Unlock()
	if h == nil {
		t.Fatal("setup: the session is not hosted")
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

// A serve that cannot listen (the port is taken) brings nothing back and
// leaves the list for the next one. It used to bring the sessions back
// first, in runs the exit then killed, and consume the list.
func TestK13AServeThatCannotListenLeavesTheListAlone(t *testing.T) {
	s, project := newTestServer(t)
	sess := newSession(t, project)
	_ = sess.Close()
	s.writeHosted([]string{sess.ID}, time.Now().UTC())
	got := stubRestore(t, nil, nil)
	taken, err := net.Listen("tcp", "127.0.0.1:0") // another program has the port
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	if err := ListenAndServe(context.Background(), taken.Addr().String(), New(project, nil)); err == nil {
		t.Fatal("served on a port that was taken")
	}
	if len(*got) != 0 {
		t.Fatalf("brought back %+v in a serve that could not listen", *got)
	}
	if listed := k3Listed(t, project); len(listed) != 1 || listed[0] != sess.ID {
		t.Fatalf("the list the next serve reads holds %q, want the session", listed)
	}
}
