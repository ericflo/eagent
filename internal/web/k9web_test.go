package web

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/store"
)

// A Stop during the shutdown for a session whose run here is slow to
// finish kills the agents before it waits for the run (their terminals
// are named on the record already): if the process exits during the wait,
// they are stopped. Their relay.end is recorded once the run has let go.
// The kill used to come after the wait.
func TestK9AStopKillsTheAgentsBeforeItWaitsForTheRun(t *testing.T) {
	s, project := newTestServer(t)
	cfg := k1Model(t, s, project)
	kills := make(chan string, 8)
	old := killAgent
	killAgent = func(host, target string) error { kills <- host + " " + target; return nil }
	t.Cleanup(func() { killAgent = old })
	info := srAgentSession(t, project)
	// A run that is ending (the shutdown is ending it), standing in for the
	// one here on this session; the test holds the session's lock for it.
	rt, ui := r11Ended(t, cfg, project, r11Session(t, project))
	lock, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	slow := &hosted{rt: rt, ui: ui, cancel: func() {}, done: make(chan struct{})}
	s.mu.Lock()
	s.running[info.ID] = slow
	s.mu.Unlock()
	s.rememberHosted() // the shutdown begins: the mark, then the list
	code, stopDone := make(chan int, 1), make(chan struct{})
	go func() {
		code <- do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil).Code
		close(stopDone)
	}()
	k7Waiting(t, "web.(*Server).stopAgentsOf", stopDone) // it waits for the run
	var before []string
	for len(kills) > 0 {
		before = append(before, <-kills)
	}
	_ = lock.Close() // the run lets go at last
	s.mu.Lock()
	delete(s.running, info.ID)
	s.mu.Unlock()
	close(slow.done)
	if c := <-code; c != http.StatusOK {
		t.Fatalf("stop: %d", c)
	}
	if !slices.Equal(before, []string{"tmux eagent-sr-claude"}) {
		t.Fatalf("killed %q before waiting for the run, want the agent", before)
	}
	if len(kills) != 0 {
		t.Fatalf("killed %q again after the run let go, want each agent once", <-kills)
	}
	if got := srActiveAgents(t, info); len(got) != 0 {
		t.Fatalf("the record still shows %q running", got)
	}
}
