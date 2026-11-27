package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// sr10Host is a reply watcher's host that runs nothing and notes each
// resume asked of it.
type sr10Host struct{ resumed chan string }

func (h *sr10Host) Hosted(string) bool { return false }
func (h *sr10Host) Resume(id string, msg integration.PhoneMessage) error {
	h.resumed <- msg.ID + ":" + msg.Text
	return nil
}

// sr10Thread is a phone service holding one thread "t" (the session's)
// with one message from the user, R, written while eagent serve was down.
// A post to the thread becomes its newest message (preview_sender of the
// poster), as on FinaleChat.
type sr10Thread struct {
	mu      sync.Mutex
	ext     string
	rAt     time.Time
	preview string
	last    time.Time
	posts   []string
	events  chan struct{}
}

func (f *sr10Thread) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	switch {
	case p == "/api/v1/events":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		select {
		case f.events <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	case p == "/api/v1/me":
		_, _ = w.Write([]byte(`{"features":["idempotency"]}`))
	case p == "/api/v1/threads":
		f.mu.Lock()
		out := map[string]any{"threads": []map[string]any{{"id": "t", "external_id": f.ext, "preview_sender": f.preview, "last_activity_at": f.last.Format(time.RFC3339Nano)}}}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/messages"):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		text, _ := body["body"].(string)
		sender, _ := body["sender"].(string)
		f.mu.Lock()
		f.posts = append(f.posts, text)
		f.preview, f.last = sender, time.Now().UTC()
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"message":{"id":"n"},"thread":{"id":"t"}}`))
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/messages"):
		q := r.URL.Query()
		if q.Get("after") != "" || q.Get("before") != "" {
			_, _ = w.Write([]byte(`{"messages":[]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]any{{"id": "R", "sender": "user", "origin": "session", "body": "actually, carry on", "created_at": f.rAt.Format(time.RFC3339Nano), "meta": map[string]any{}}}})
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/api/v1/threads/"):
		_ = json.NewEncoder(w).Encode(map[string]any{"thread": map[string]any{"id": "t", "external_id": f.ext}})
	default:
		_, _ = w.Write([]byte(`{}`))
	}
}

// The user pressed Stop on a session while `eagent serve` was stopping,
// and the process exited before the stop was finished: the session is
// marked on the list. While eagent was down the user wrote to the
// session's thread from the phone (R): "A reply here resumes it while
// eagent serve is running". The next serve finishes the marked stop at
// its start (restoreHosted, before the reply watcher starts) and tells
// the phone, posting the closing note after R. The reply watcher's sweep,
// the only thing that finds a reply sent while nothing watched, skips a
// thread whose newest message is not the user's: R is never taken, and
// nobody is told, until the user writes again.
func TestSR10WebAReplyWhileDownIsHiddenByTheStopFinishedAtStart(t *testing.T) {
	posts, resumed := sr10ReplyWhileDown(t, true)
	if resumed == "" {
		t.Fatalf("the reply the user sent while eagent was down (R) was not taken: the stop finished at the start posted %q after it, and the sweep skipped the thread; the session stays stopped with R unread and nobody told", posts)
	}
}

// The control: the same session and reply, with no stop left to finish
// (the session was simply stopped before the restart). The sweep finds R
// and resumes the session with it.
func TestSR10WebAReplyWhileDownControlIsTaken(t *testing.T) {
	if _, resumed := sr10ReplyWhileDown(t, false); resumed != "R:actually, carry on" {
		t.Fatalf("control: resumed with %q, want R", resumed)
	}
}

func sr10ReplyWhileDown(t *testing.T, marked bool) (posts []string, resumed string) {
	s, project := newTestServer(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	f := &sr10Thread{events: make(chan struct{}, 1)}
	phone := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(phone.Close)
	t.Setenv("FINALECHAT_TOKEN", "fc_test")
	t.Setenv("FINALECHAT_URL", phone.URL)

	// A session this build ran (it keeps the phone ledger), mirrored to
	// the thread "t", stopped by the user during the last shutdown.
	sess := newSession(t, project)
	for _, ev := range []event.Event{
		event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: sess.ID, Cwd: project, PhoneLedger: true}),
		event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", ExternalID: "eagent:" + sess.ID, BaseURL: phone.URL}),
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
	f.ext = "eagent:" + info.ID
	if marked {
		s.mu.Lock()
		s.writeHostedList(nil, time.Now().UTC(), []stopMark{{Session: info.ID, At: time.Now().UTC()}})
		s.mu.Unlock()
	}

	// Downtime: the user replies in the thread.
	f.mu.Lock()
	f.rAt = time.Now().Add(time.Second).UTC()
	f.preview, f.last = "user", f.rAt
	f.mu.Unlock()

	// The next serve starts: the restore, then the reply watcher.
	next := New(project, nil)
	next.restoreHosted(context.Background())
	f.mu.Lock()
	posts = append([]string(nil), f.posts...)
	f.mu.Unlock()
	if marked && len(posts) == 0 {
		t.Fatal("setup: the stop finished at the start told the phone nothing")
	}
	host := &sr10Host{resumed: make(chan string, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	stop := integration.StartResumeWatcher(ctx, project, host, func(string, ...any) {})
	select {
	case <-f.events: // the first sweep is done
	case <-time.After(20 * time.Second):
		cancel()
		stop()
		t.Fatal("setup: the reply watcher never swept")
	}
	cancel()
	stop()
	select {
	case resumed = <-host.resumed:
	default:
	}
	return posts, resumed
}
