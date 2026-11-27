package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// k3Listed is what the next server would bring back.
func k3Listed(t *testing.T, project string) []string {
	t.Helper()
	raw, err := os.ReadFile(hostedFile(project))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var list hostedList
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	return list.Sessions
}

// A session the user stops while the server is stopping is not brought
// back by the next server: the list written at the start of the shutdown
// loses it. One the shutdown stops stays on it. The user's used to stay
// too, and the next serve resumed it.
func TestK3ASessionTheUserStopsDuringTheShutdownIsNotBroughtBack(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	stopped, kept := r11Session(t, project), r11Session(t, project)
	_, h := k2Hosted(t, s, cfg, project, stopped)
	k2Hosted(t, s, cfg, project, kept)
	s.rememberHosted() // as ListenAndServe does when its shutdown begins
	if got := k3Listed(t, project); !slices.Equal(got, sortedIDs(stopped.ID, kept.ID)) {
		t.Fatalf("listed %q", got)
	}
	if w := do(s, "POST", "/api/sessions/"+stopped.ID+"/stop", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	<-h.done
	s.Shutdown(5 * time.Second)
	if got := k3Listed(t, project); !slices.Equal(got, []string{kept.ID}) {
		t.Fatalf("the next server would bring back %q, want only %s (the user stopped %s)", got, kept.ID, stopped.ID)
	}
}

// A Stop pressed once the server's shutdown has begun ending the run, or
// has ended it, is a stop: the session comes off the list the next server
// brings back, and the answer says it is stopped. It used to be refused
// (409), and the next serve brought the session back.
func TestK3AStopDuringTheShutdownIsHonoured(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	ending, ended := r11Session(t, project), r11Session(t, project)
	rt, ui := r11Ended(t, cfg, project, ending)
	lock, _, err := store.Open(ending.Path, time.Now()) // the run is finishing
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	s.mu.Lock()
	s.running[ending.ID] = &hosted{rt: rt, ui: ui, cancel: func() {}, done: make(chan struct{})}
	s.running[ended.ID] = &hosted{rt: rt, ui: ui, cancel: func() {}, done: make(chan struct{})}
	s.mu.Unlock()
	t.Cleanup(func() { s.mu.Lock(); delete(s.running, ending.ID); s.mu.Unlock() })
	s.rememberHosted() // the shutdown begins: the list, then the mark
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
	s.mu.Lock()
	delete(s.running, ended.ID) // its run has stopped
	s.mu.Unlock()
	for _, info := range []store.Info{ending, ended} {
		w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("stop of %s: %d %s", info.ID, w.Code, w.Body)
		}
	}
	if got := k3Listed(t, project); len(got) != 0 {
		t.Fatalf("the next server would bring back %q, which the user stopped", got)
	}
}

func sortedIDs(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

// A reply from the phone for a session already running is answered with
// ErrRunning, which the reply watcher takes as left to the session's own
// mirror (when the browser resumed the session first, say).
func TestK3ResumeOfASessionRunningIsErrRunning(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	info := r11Session(t, project)
	k2Hosted(t, s, cfg, project, info)
	if err := s.Resume(info.ID, integration.PhoneMessage{ID: "m1", Text: "from the phone"}); !errors.Is(err, ErrRunning) {
		t.Fatalf("Resume: %v, want ErrRunning", err)
	}
}
