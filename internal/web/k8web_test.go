package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
	"github.com/ericflo/eagent/internal/store"
)

// A Dismiss sent while a Stop during the shutdown is stopping the session's
// agents is refused: the session is locked by that stop, not run. It used
// to be put in its inbox and answered "delivered".
func TestK8ADismissDuringAStopInTheShutdownIsRefused(t *testing.T) {
	s, project := newTestServer(t)
	info := srAgentSession(t, project)
	entered, gate := make(chan struct{}), make(chan struct{})
	old := killAgent
	killAgent = func(host, target string) error { close(entered); <-gate; return nil }
	t.Cleanup(func() { killAgent = old })
	k7Shutdown(t, s, info)
	stopped := make(chan struct{})
	go func() { do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil); close(stopped) }()
	<-entered // the stop holds the session's lock
	w := do(s, "POST", "/api/sessions/"+info.ID+"/dismiss", map[string]any{"question_id": "q1"}, nil)
	close(gate)
	<-stopped
	if left, _ := os.ReadDir(harness.InboxDir(info.Path)); w.Code != http.StatusConflict || len(left) != 0 {
		t.Fatalf("answered %d %s, %d file(s) left in the inbox of a session nothing will run", w.Code, w.Body, len(left))
	}
}

// A message held for the end of the agent's turn waits in the agent's
// spool (its Stop hook hands it over) when the server's shutdown ends the
// run. The user presses Stop: the agent is killed, so its hook never takes
// the message. A stop the session takes (settleExited, fb011ca) takes it
// back and records it not_sent; the Stop during the shutdown (stopAgents)
// records it unconfirmed ("whether it arrived cannot be confirmed") and
// leaves it in the spool, where nothing will ever take it.
func TestSR3WebAStopDuringTheShutdownCallsAMessageStillInTheSpoolUnconfirmed(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "h1", From: "user", Mode: "after_turn", Text: "then run the linter"})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	spool := filepath.Join(info.Path, "relays", "claude", "after_turn")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(spool, "1700000000000000000-h1.txt")
	if err := os.WriteFile(held, []byte("then run the linter"), 0o644); err != nil {
		t.Fatal(err)
	}
	k7Shutdown(t, s, info)
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	var outcomes []string
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == "h1" {
			outcomes = append(outcomes, d.Outcome+": "+d.Detail)
		}
	}
	_, still := os.Stat(held)
	if len(outcomes) != 1 || !strings.HasPrefix(outcomes[0], event.DeliveryNotSent) || still == nil {
		t.Fatalf("the message never left the spool and its agent was killed; its outcomes on record: %q; still in the spool: %v", outcomes, still == nil)
	}
}
