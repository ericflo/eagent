package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// sr12Outcomes is each send's outcomes on the record, by id.
func sr12Outcomes(t *testing.T, info store.Info) map[string]string {
	t.Helper()
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil {
			got[d.ID] += d.Outcome
		}
	}
	return got
}

// The agent was stopped in the run (relay.end), the user had it started
// again, and a message of theirs was held for that start (the record marks
// it held, with no start after it) when eagent serve was shut down. A Stop
// from outside the run settles the session's sends as a resume would: a
// resume settles this one not_sent first, "whatever became of its agent"
// (harness reattachRelays, state.NeverTyped). stopAgents reads only the
// active agents, so it is left with no outcome, and the phone it then
// tells shows it sent.
func TestSR12AStopFromOutsideARunSettlesASendHeldForTheRestartOfAStoppedAgent(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude", Reason: "the user stopped it"})); err != nil {
		t.Fatal(err)
	}
	um, err := sess.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "run the tests", Source: "web"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Seq: um.Seq, Text: "run the tests", Auto: true, Held: true})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	evs, err := store.Read(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if never := state.NeverTyped(evs); len(never) != 1 || never[0].ID != "r1" {
		t.Fatalf("NeverTyped %v; the record itself says r1 was never typed", never)
	}
	k7Shutdown(t, s, info)
	if w := do(s, "POST", "/api/sessions/"+info.ID+"/stop", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	if got := sr12Outcomes(t, info); got["r1"] != event.DeliveryNotSent {
		t.Fatalf("outcomes %v; want r1 not_sent, as a resume settles it (it was held for a start that never came up)", got)
	}
}

// The run stopped the agent (relay.end) and was cut before that stop's
// settle. A resume settles what the stopped agent still waited on as
// unconfirmed (harness settleEnded); a Stop from outside the run leaves it
// waiting on an agent that is gone.
func TestSR12BStopFromOutsideARunSettlesWhatAStoppedAgentStillWaitedOn(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Text: "run the tests"}),
		event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude", Reason: "the user stopped it"}),
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
	if got := sr12Outcomes(t, info); got["r1"] == "" {
		t.Fatalf("outcomes %v; want r1 settled (unconfirmed, as a resume settles it)", got)
	}
}
