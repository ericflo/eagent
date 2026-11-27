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

// k7Shutdown puts the server where its shutdown has ended the session's
// run: the session on the list, the mark set.
func k7Shutdown(t *testing.T, s *Server, info store.Info) {
	t.Helper()
	s.mu.Lock()
	s.writeHosted([]string{info.ID}, time.Now())
	s.mu.Unlock()
	s.resuming.Lock()
	s.stopping = true
	s.resuming.Unlock()
}

// A Stop during the shutdown settles the sends its agent had not yet shown
// arriving, as a stop the session takes does: each is reported
// unconfirmed, once. They used to stay waiting on an agent that had been
// stopped.
func TestK7AStopDuringTheShutdownSettlesTheAgentsSends(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Mode: "queue", Text: "run the tests"})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	k7Shutdown(t, s, info)
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	var outcomes []event.RelayDeliveredData
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == "r1" {
			outcomes = append(outcomes, d)
		}
	}
	if len(outcomes) != 1 || outcomes[0].OK || outcomes[0].Outcome != event.DeliveryUnconfirmed {
		t.Fatalf("the send's outcomes on record: %+v, want one, unconfirmed", outcomes)
	}
}
