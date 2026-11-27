package web

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// A Stop the server's shutdown overtook (or the next server finishing it)
// takes a message held for the end of Claude's turn back out of the spool
// before it kills the agent (it must: the seat sweeps the spool once the
// pane is gone), and records not_sent only after the kill and the
// relay.end. The serve exits during that kill (its shutdown is bounded and
// the stop runs at its very end), so the stop is cut there: the message is
// already out of the spool, nothing is recorded. The next server finishes
// the stop (the mark is still on the list): the agent is now ended and
// still waited on (harness.EndedWaiting), its spool no longer holds the
// message, and it is recorded "unconfirmed: the agent was stopped before
// its arrival showed in its transcript" - for a message that was taken
// back unread and certainly never reached the agent. The phone keeps it
// shown as sent (no Retry), and the model is told it may have arrived.
func TestSR13AStopCutDuringTheKillRecordsATakenBackMessageUnconfirmed(t *testing.T) {
	_, project := newTestServer(t)
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

	// The first stop: the process ends while the agent's terminal is being
	// killed (Goexit stands for the exit: the session's lock is let go, and
	// nothing after the kill runs).
	old := killAgent
	t.Cleanup(func() { killAgent = old })
	killAgent = func(string, string) error { runtime.Goexit(); return nil }
	cut := make(chan struct{})
	go func() {
		defer close(cut)
		_, _, _ = stopAgents(project, info.Path, nil, nil)
	}()
	select {
	case <-cut:
	case <-time.After(20 * time.Second):
		t.Fatal("the first stop did not end")
	}
	if _, err := os.Stat(held); err == nil {
		t.Fatal("setup: the first stop did not take the message back")
	}

	// The next server finishes the stop.
	killAgent = func(string, string) error { return nil }
	if _, _, err := stopAgents(project, info.Path, nil, nil); err != nil {
		t.Fatal(err)
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
	if len(outcomes) != 1 || !strings.HasPrefix(outcomes[0], event.DeliveryNotSent) {
		t.Fatalf("the message taken back unread from the spool, never delivered, is on record as %q; want one not_sent", outcomes)
	}
}
