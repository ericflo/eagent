package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// The agent was stopped (relay_control stop) and eagent was cut before the
// stop's settle ran (its wait for the stopped agent's last word). A resume
// settles the send it was waiting on (settleEnded: unconfirmed). A Stop
// from outside any run leaves it with no outcome: stopAgents walks only the
// active relays.
func TestSR12StopFromOutsideSettlesAStoppedRelaysCutSettle(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "s1", From: "user", Seq: 1, Mode: "queue", Text: "run the tests"}),
		event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude", Reason: "stopped"}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
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
	got := ""
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == "s1" {
			got += d.Outcome
		}
	}
	if got != event.DeliveryUnconfirmed {
		t.Fatalf("send s1 to the stopped agent: outcome %q; want unconfirmed, as a resume settles it", got)
	}
}

// The agent was stopped, then started again under its name (relay_control
// restart of a stopped relay), and a user message was held for that start
// (the record marks it held). eagent was cut before the agent came up. A
// Stop from outside any run should settle the held send not_sent, as a
// resume would (state.NeverTyped); stopAgents walks only the active
// relays, so the send is left with no outcome at all.
func TestSR12StopFromOutsideSettlesAHeldSendOfAStoppedRelay(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude", Reason: "stopped"}),
		event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "s2", From: "user", Mode: "queue", Text: "and the linter", Held: true}),
	} {
		if _, err := sess.Append(ev); err != nil {
			t.Fatal(err)
		}
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
	got := ""
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == "s2" {
			got += d.Outcome
		}
	}
	if got != event.DeliveryNotSent {
		t.Fatalf("held send s2 outcome %q; want not_sent (held for a start that never came up)", got)
	}
}
