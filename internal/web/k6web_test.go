package web

import (
	"net/http"
	"testing"
)

// Once the list of sessions to bring back is taken, at the start of the
// shutdown, no run starts here: one that did would be on no list, and its
// agents left running by the shutdown. A new session asked for in that
// instant used to start.
func TestK6NoRunStartsOnceTheListIsTaken(t *testing.T) {
	s, project := newTestServer(t)
	k1Model(t, s, project)
	s.rememberHosted() // as ListenAndServe does when its shutdown begins
	w := do(s, "POST", "/api/sessions", map[string]any{"prompt": "a new one"}, nil)
	s.mu.Lock()
	n := len(s.running)
	s.mu.Unlock()
	if w.Code != http.StatusServiceUnavailable || n != 0 {
		t.Fatalf("answered %d %s, %d run(s) started after the list was taken (listed: %q)", w.Code, w.Body, n, k3Listed(t, project))
	}
}
