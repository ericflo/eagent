package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ericflo/eagent/internal/integration"
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
