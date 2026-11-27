package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
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

// Two Stops at once during the shutdown: the second waits for the first
// and gets its answer. It used to fail on the session's lock, which the
// first held, with a 500 naming this server's own pid.
func TestK7TwoStopsAtOnceDuringTheShutdownGetOneAnswer(t *testing.T) {
	s, project := newTestServer(t)
	info := srAgentSession(t, project)
	entered, gate := make(chan struct{}), make(chan struct{})
	old := killAgent
	killAgent = func(host, target string) error { close(entered); <-gate; return nil }
	t.Cleanup(func() { killAgent = old })
	k7Shutdown(t, s, info)
	first, second := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil) }()
	<-entered // the first is stopping the agent, under the session's lock
	secondDone := make(chan struct{})
	go func() { second <- do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil); close(secondDone) }()
	k7Waiting(t, "web.(*Server).postStop", secondDone) // it waits for the first, or has answered
	close(gate)
	a, b := <-first, <-second
	if a.Code != http.StatusOK || b.Code != a.Code || b.Body.String() != a.Body.String() {
		t.Fatalf("first: %d %s; second: %d %s", a.Code, a.Body, b.Code, b.Body)
	}
}

// Words from the browser sent while a Stop during the shutdown is stopping
// the session's agents are refused, and the page keeps them: the session
// is locked by that stop, not run, and will not be brought back. They used
// to be put in its inbox and answered "delivered".
func TestK7WordsDuringAStopInTheShutdownAreRefused(t *testing.T) {
	s, project := newTestServer(t)
	info := srAgentSession(t, project)
	entered, gate := make(chan struct{}), make(chan struct{})
	old := killAgent
	killAgent = func(host, target string) error { close(entered); <-gate; return nil }
	t.Cleanup(func() { killAgent = old })
	k7Shutdown(t, s, info)
	stopped := make(chan *httptest.ResponseRecorder, 1)
	go func() { stopped <- do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil) }()
	<-entered // the stop holds the session's lock
	w := do(s, "POST", "/api/sessions/"+info.ID+"/message", map[string]any{"text": "one more thing"}, nil)
	close(gate)
	<-stopped
	if left, _ := os.ReadDir(harness.InboxDir(info.Path)); w.Code != http.StatusServiceUnavailable || len(left) != 0 {
		t.Fatalf("answered %d %s, %d file(s) left in the inbox of a session nothing will run", w.Code, w.Body, len(left))
	}
}

// k7Waiting waits until a goroutine is parked receiving from a channel
// within fn, or done is closed (fn went on without waiting).
func k7Waiting(t *testing.T, fn string, done <-chan struct{}) {
	t.Helper()
	buf := make([]byte, 1<<20)
	for deadline := time.Now().Add(5 * time.Second); ; runtime.Gosched() {
		select {
		case <-done:
			return
		default:
		}
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "[chan receive") && strings.Contains(g, fn) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s neither waited nor returned", fn)
		}
	}
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
