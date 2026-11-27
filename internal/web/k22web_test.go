package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// The run was cut while its agent was starting again, with a send held for
// that start (the record marks it held, and has no start after it). A Stop
// from outside any run settles it as a resume would: it was never typed,
// so it is not sent; a send from before the restart may have been typed,
// and is unconfirmed.
func TestK22AStopFromOutsideARunSaysAHeldSendWasNeverTyped(t *testing.T) {
	s, project := newTestServer(t)
	k5Kills(t)
	info := srAgentSession(t, project)
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []event.Event{
		event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "s1", From: "user", Text: "run the tests"}),
		event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "s2", From: "user", Text: "and the linter", Held: true}),
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
	got := map[string]string{}
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil {
			got[d.ID] += d.Outcome
		}
	}
	if got["s1"] != event.DeliveryUnconfirmed || got["s2"] != event.DeliveryNotSent {
		t.Fatalf("outcomes %v; want s1 unconfirmed (it may have been typed) and s2 not_sent (held for a start that never came up)", got)
	}
}
