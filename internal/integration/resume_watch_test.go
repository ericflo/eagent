package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/store"
)

type fakeResumeHost struct {
	mu      sync.Mutex
	resumed []PhoneMessage
	ids     []string
	hosted  map[string]bool
}

func (h *fakeResumeHost) Hosted(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hosted[id]
}
func (h *fakeResumeHost) Resume(id string, msg PhoneMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ids = append(h.ids, id)
	h.resumed = append(h.resumed, msg)
	return nil
}
func (h *fakeResumeHost) calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.resumed)
}

// finishedSession creates a session directory nobody is running.
func finishedSession(t *testing.T, project string, at time.Time) string {
	t.Helper()
	sess, err := store.Create(store.Root(project), at)
	if err != nil {
		t.Fatal(err)
	}
	id := sess.ID
	sess.Close()
	return id
}

func sseEvent(w http.ResponseWriter, name string, data any) {
	raw, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, raw)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestReplyToFinishedSessionResumesItOnce(t *testing.T) {
	project := t.TempDir()
	dead := finishedSession(t, project, time.Now().Add(-2*time.Minute))
	live, err := store.Create(store.Root(project), time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close() // holds the lock: a running session
	thread := func(sid string) map[string]any {
		return map[string]any{"id": "t-" + sid, "external_id": "eagent:" + sid}
	}
	message := func(id, body, origin string) map[string]any {
		return map[string]any{"id": id, "thread_id": "t", "sender": "user", "body": body, "origin": origin, "created_at": time.Now()}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fc_fixture" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/threads":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"threads":[]}`))
		case r.URL.Path == "/api/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			sseEvent(w, "ready", map[string]any{})
			sseEvent(w, "message.created", map[string]any{"thread": thread(dead), "message": message("m1", "  continue please ", "session")})
			sseEvent(w, "message.created", map[string]any{"thread": thread(dead), "message": message("m1", "continue please", "session")})    // duplicate delivery
			sseEvent(w, "message.created", map[string]any{"thread": thread(dead), "message": message("m2", "mirrored by an agent", "token")}) // an agent's mirror of terminal input
			sseEvent(w, "message.created", map[string]any{"thread": thread(live.ID), "message": message("m3", "for a running session", "session")})
			sseEvent(w, "message.created", map[string]any{"thread": map[string]any{"id": "x", "external_id": "claude-code:abc"}, "message": message("m4", "someone else's", "session")})
			sseEvent(w, "message.created", map[string]any{"thread": thread(dead), "message": map[string]any{"id": "m5", "sender": "agent", "body": "agent text", "origin": "token"}})
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_URL", server.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	host := &fakeResumeHost{hosted: map[string]bool{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- watchResumes(ctx, project, host, t.Logf) }()
	deadline := time.Now().Add(5 * time.Second)
	for host.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // let the rest of the burst arrive
	cancel()
	<-done
	if host.calls() != 1 || host.ids[0] != dead || host.resumed[0].Text != "continue please" || host.resumed[0].ID != "m1" {
		t.Fatalf("unexpected resumes: ids=%v msgs=%#v", host.ids, host.resumed)
	}
}

func TestSweepFindsRepliesSentWhileNothingWatched(t *testing.T) {
	project := t.TempDir()
	stale := finishedSession(t, project, time.Now().Add(-3*time.Minute)) // replied to after its last log write
	current := finishedSession(t, project, time.Now().Add(-2*time.Minute))
	// The reply to `current` predates the session's log, so the session saw it.
	replied := time.Now().Add(time.Minute)
	earlier := time.Now().Add(-time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/threads":
			threads := []map[string]any{
				{"id": "t1", "external_id": "eagent:" + stale, "preview_sender": "user", "last_activity_at": replied},
				{"id": "t2", "external_id": "eagent:" + current, "preview_sender": "user", "last_activity_at": earlier},
				{"id": "t3", "external_id": "eagent:" + stale, "preview_sender": "agent", "last_activity_at": replied},
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"threads": threads})
		case strings.HasPrefix(r.URL.Path, "/api/v1/threads/ext:eagent:"+stale+"/messages"):
			msgs := []map[string]any{
				{"id": "old", "sender": "user", "body": "first prompt", "origin": "token", "created_at": earlier},
				{"id": "new", "sender": "user", "body": "are you still there?", "origin": "session", "created_at": replied},
			}
			if r.URL.Query().Get("after") != "" {
				msgs = nil
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": msgs})
		case r.URL.Path == "/api/v1/events":
			http.Error(w, "no stream in this fixture", 503)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_URL", server.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	host := &fakeResumeHost{hosted: map[string]bool{}}
	err := watchResumes(context.Background(), project, host, t.Logf)
	if err == nil {
		t.Fatal("expected the missing stream to end the watch")
	}
	if host.calls() != 1 || host.ids[0] != stale || host.resumed[0].Text != "are you still there?" {
		t.Fatalf("unexpected resumes: ids=%v msgs=%#v", host.ids, host.resumed)
	}
}

func TestResumeWatcherIsOffWithTheKillSwitch(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "off")
	stop := StartResumeWatcher(context.Background(), t.TempDir(), &fakeResumeHost{hosted: map[string]bool{}}, nil)
	stop()
}

func TestElectedResumeWatcherStartsInAFreshProject(t *testing.T) {
	project := t.TempDir()
	dead := finishedSession(t, project, time.Now().Add(-2*time.Minute))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/threads":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"threads":[]}`))
		case "/api/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			sseEvent(w, "ready", map[string]any{})
			sseEvent(w, "message.created", map[string]any{"thread": map[string]any{"id": "t", "external_id": "eagent:" + dead}, "message": map[string]any{"id": "m1", "sender": "user", "body": "hello again", "origin": "session"}})
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_URL", server.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	host := &fakeResumeHost{hosted: map[string]bool{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := StartResumeWatcher(ctx, project, host, t.Logf)
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for host.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if host.calls() != 1 || host.resumed[0].Text != "hello again" {
		t.Fatalf("the elected watcher did not resume the session: %#v", host.resumed)
	}
}
