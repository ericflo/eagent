package web

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// A Stop pressed during the shutdown for a session whose agent had already
// exited (its record: relay.state exited, no relay.end) asks tmux to kill a
// terminal that is gone, and tmux answers "can't find session". The answer
// then says the agent could not be stopped, which reads as an agent still
// running, where the agent had exited. stopSession, for the same record,
// only logs the failed kill.
func TestSR2WebAStopDuringTheShutdownSaysAnExitedAgentCouldNotBeStopped(t *testing.T) {
	s, project := newTestServer(t)
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "exited", Detail: "the process ended"})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	old := killAgent
	killAgent = func(host, target string) error {
		return errors.New("can't find session: " + target) // tmux, for a terminal that is gone
	}
	t.Cleanup(func() { killAgent = old })
	s.mu.Lock()
	s.writeHosted([]string{info.ID}, time.Now())
	s.mu.Unlock()
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
	w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "Could not stop") {
		t.Fatalf("the agent had exited, and the answer says it could not be stopped: %s", strings.TrimSpace(w.Body.String()))
	}
	if got := srActiveAgents(t, info); len(got) != 0 {
		t.Fatalf("the record still shows %q running", got)
	}
}
