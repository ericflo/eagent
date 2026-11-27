package web

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/store"
)

// A Stop the shutdown overtook was finished; then another process (a
// terminal's `eagent -c`) resumed the session while this server was still
// stopping. The lock on the session is that run's, not the finished
// stop's: a new Stop reaches its inbox, as a Stop for a session another
// process runs does (and as on main), and so do a dismissal and words.
// (Each was answered by the finished stop: "Stopped", "the session is
// stopping", "send it again once it is back".)
func TestK28AStopForASessionAnotherProcessResumedAfterAShutdownStop(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	k7Shutdown(t, s, info)
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("first stop: %d %s", w.Code, w.Body)
	}
	// Another process resumes the session (it holds the lock).
	other, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if !alive(info.Path) {
		t.Fatal("setup: the session is not locked")
	}
	inbox := func(kind string) int {
		entries, _ := os.ReadDir(harness.InboxDir(info.Path))
		n := 0
		for _, e := range entries {
			raw, _ := os.ReadFile(harness.InboxDir(info.Path) + "/" + e.Name())
			if strings.Contains(string(raw), kind) {
				n++
			}
		}
		return n
	}
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil); w.Code != http.StatusOK || inbox(`"stop"`) != 1 || strings.Contains(w.Body.String(), "Stopped") {
		t.Errorf("a Stop for a session another process runs: %d %s, with %d stop files in its inbox; want it handed to that run", w.Code, strings.TrimSpace(w.Body.String()), inbox(`"stop"`))
	}
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/dismiss", map[string]any{"question_id": "q1"}, nil); w.Code != http.StatusOK || inbox(`"dismiss"`) != 1 {
		t.Errorf("a dismissal for a session another process runs: %d %s, with %d in its inbox; want it handed to that run", w.Code, strings.TrimSpace(w.Body.String()), inbox(`"dismiss"`))
	}
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": "one more thing"}, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "queued") || inbox("one more thing") != 1 {
		t.Errorf("words for a session another process runs: %d %s, with %d in its inbox; want them queued for that run", w.Code, strings.TrimSpace(w.Body.String()), inbox("one more thing"))
	}
}
