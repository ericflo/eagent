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

	"github.com/ericflo/eagent/internal/integration"
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
