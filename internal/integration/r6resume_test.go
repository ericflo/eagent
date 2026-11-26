package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/finalechat"
	"github.com/ericflo/eagent/internal/store"
)

// r6Thread is a fake service with one thread per session: its user
// messages, paged after an anchor in order, as FinaleChat pages them.
type r6Thread struct {
	mu      sync.Mutex
	srv     *httptest.Server
	threads map[string][]map[string]any // session id -> messages, oldest first
	pages   int
}

func newR6Thread(t *testing.T) *r6Thread {
	t.Helper()
	f := &r6Thread{threads: map[string][]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/api/v1/threads":
			var out []map[string]any
			for sid, msgs := range f.threads {
				last := msgs[len(msgs)-1]
				out = append(out, map[string]any{"id": "t-" + sid, "external_id": "eagent:" + sid, "preview_sender": last["sender"], "last_activity_at": last["created_at"]})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"threads": out})
		case strings.HasPrefix(r.URL.Path, "/api/v1/threads/ext:eagent:") && strings.HasSuffix(r.URL.Path, "/messages") && r.Method == "GET":
			f.pages++
			sid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/threads/ext:eagent:"), "/messages")
			msgs := f.threads[sid]
			after := r.URL.Query().Get("after")
			start, unknown := 0, false
			if after != "" {
				unknown = true
				for i, m := range msgs {
					if m["id"] == after {
						start, unknown = i+1, false
					}
				}
			}
			out := []map[string]any{}
			if !unknown {
				for _, m := range msgs[start:] {
					if s := r.URL.Query().Get("sender"); s == "" || m["sender"] == s {
						out = append(out, m)
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": out, "anchor_unknown": unknown})
		case r.Method == "POST":
			_, _ = w.Write([]byte(`{"message":{"id":"note"},"thread":{"id":"t"}}`))
		case r.URL.Path == "/api/v1/events":
			http.Error(w, "no stream in this fixture", 503)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// say adds a message the person wrote from the app, at a time.
func (f *r6Thread) say(sid, id, body string, at time.Time, meta map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if meta == nil {
		meta = map[string]any{}
	}
	f.threads[sid] = append(f.threads[sid], map[string]any{"id": id, "sender": "user", "origin": "session", "body": body, "created_at": at, "meta": meta})
}

func (f *r6Thread) watcher(project string, host ResumeHost, t *testing.T) *resumeWatcher {
	return &resumeWatcher{project: project, host: host, client: &finalechat.Client{BaseURL: f.srv.URL, Token: "fc_fixture"}, logf: t.Logf,
		retries: &replyRetries{replies: map[string]pendingReply{}}, handled: map[string]time.Time{}}
}

func r6Info(t *testing.T, project, sid string) store.Info {
	t.Helper()
	info, err := store.Resolve(store.Root(project), sid)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// Two messages sent from the app while the session was stopped: the sweep
// resumes the session with the earlier one; the session's poll reads on
// from it and takes the later one in turn.
func TestTheSweepResumesFromTheEarliestMessageSentWhileStopped(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "m1", "the checklist is A, B, C", stop.Add(time.Minute), nil)
	f.say(sid, "m2", "are you still there?", stop.Add(2*time.Minute), nil)
	host := &fakeResumeHost{hosted: map[string]bool{}}
	f.watcher(project, host, t).sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m1" || host.resumed[0].Text != "the checklist is A, B, C" {
		t.Fatalf("resumed with %#v", host.resumed)
	}
}

// Restoring a hosted session after a restart of the serve picks the same
// message the sweep would.
func TestARestoredSessionPicksTheEarliestMessageSentWhileStopped(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "m1", "the checklist is A, B, C", stop.Add(time.Minute), nil)
	f.say(sid, "m2", "are you still there?", stop.Add(2*time.Minute), nil)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("FINALECHAT_URL", f.srv.URL)
	t.Setenv("FINALECHAT_TOKEN", "fc_fixture")
	msg, ok, err := PendingReply(context.Background(), project, r6Info(t, project, sid))
	if err != nil || !ok || msg.ID != "m1" {
		t.Fatalf("pending reply %#v ok=%v err=%v", msg, ok, err)
	}
}
