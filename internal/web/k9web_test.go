package web

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// A Stop during the shutdown for a session whose run here is slow to
// finish kills the agents only once the run has let go, from the record as
// it then stands, each once. (Changed with the stop's redesign: it used to
// pin a kill before the wait, which killed an agent a restart was
// replacing and let a still-watching seat sweep the spool.)
func TestK9AStopKillsTheAgentsOnceTheRunHasLetGo(t *testing.T) {
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
	if len(kills) != 0 {
		t.Fatalf("killed %q before the run let go", <-kills)
	}
	_ = lock.Close() // the run lets go at last
	s.mu.Lock()
	delete(s.running, info.ID)
	s.mu.Unlock()
	close(slow.done)
	if c := <-code; c != http.StatusOK {
		t.Fatalf("stop: %d", c)
	}
	var after []string
	for len(kills) > 0 {
		after = append(after, <-kills)
	}
	if !slices.Equal(after, []string{"tmux eagent-sr-claude"}) {
		t.Fatalf("killed %q once the run let go, want the agent once", after)
	}
	if got := srActiveAgents(t, info); len(got) != 0 {
		t.Fatalf("the record still shows %q running", got)
	}
}

// When the server exits while such a Stop waits, the list it leaves marks
// the stop, and the next server finishes it (the agents the record shows
// running stopped and recorded) and does not bring the session back.
func TestK9TheNextServerFinishesAStopTheLastOneLeft(t *testing.T) {
	s, project := newTestServer(t)
	kills := make(chan string, 8)
	old := killAgent
	killAgent = func(host, target string) error { kills <- host + " " + target; return nil }
	t.Cleanup(func() { killAgent = old })
	info := srAgentSession(t, project)
	s.mu.Lock()
	s.writeHostedList([]string{info.ID}, time.Now(), k12Marks(time.Now(), info.ID))
	s.mu.Unlock()
	var resumed []string
	oldRestore := restoreSession
	restoreSession = func(_ *Server, id string, _ integration.PhoneMessage) error {
		resumed = append(resumed, id)
		return nil
	}
	t.Cleanup(func() { restoreSession = oldRestore })
	next := New(project, nil)
	next.restoreHosted(context.Background())
	if len(resumed) != 0 {
		t.Fatalf("the next server brought back %q, which the user had stopped", resumed)
	}
	if len(kills) != 1 || <-kills != "tmux eagent-sr-claude" {
		t.Fatalf("the agent was not stopped")
	}
	if got := srActiveAgents(t, info); len(got) != 0 {
		t.Fatalf("the record still shows %q running", got)
	}
}
