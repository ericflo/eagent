package state

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Each send keeps how it ended, from the record alone: a replay (a stop and
// resume) tells the same, a restart of the agent does not forget it, the
// newest word on a send wins, and a failure that does not say how sure it is
// (a record from before outcomes) may have gone.
func TestEachSendKeepsHowItEndedAcrossReplay(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	send := func(id, mode string) {
		b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: id, From: "user", Seq: 3, Mode: mode, Text: "check the version"}))
	}
	delivered := func(id string, ok bool, outcome string) {
		b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: id, OK: ok, Outcome: outcome, Detail: "x"}))
	}
	send("r1", "queue")
	delivered("r1", false, event.DeliveryNotSent)
	send("r2", "queue")
	delivered("r2", false, "")
	send("r3", "queue")
	delivered("r3", false, event.DeliveryUnconfirmed)
	delivered("r3", true, "") // shown late: it arrived after all
	send("r4", "after_turn")
	send("r5", "queue")
	// A restart of the same agent: what was on its way is not forgotten.
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", Dir: "/p", Resumed: true}))
	send("r6", "queue")
	delivered("r6", false, "something new")
	st := Replay(b.events)
	for id, want := range map[string]SendOutcome{
		"r1": SendNotSent, "r2": SendUnconfirmed, "r3": SendArrived, "r4": SendHeld, "r5": SendWaiting, "r6": SendUnconfirmed, "r9": "",
	} {
		if got := st.SendOutcome(id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
}

// Only a record that says nothing was typed renders differently in the
// model's conversation: a failure written before outcomes existed renders
// as it always did, so an existing session's prompt does not change.
func TestOnlyANotSentFailureRendersAsDidNotGo(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	for _, id := range []string{"r1", "r2", "r3"} {
		b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: id, From: "eagent", Mode: "queue", Text: "t"}))
	}
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: "r1", OK: false, Detail: "never showed up"}))
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: "r2", OK: false, Outcome: event.DeliveryUnconfirmed, Detail: "never showed up"}))
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: "r3", OK: false, Outcome: event.DeliveryNotSent, Detail: "the agent is not running"}))
	all := texts(Replay(b.events).View(nil))
	for _, want := range []string{
		"A message to Claude (r1) could not be confirmed: never showed up]",
		"A message to Claude (r2) could not be confirmed: never showed up]",
		"A message to Claude (r3) did not go; nothing of it reached the agent: the agent is not running]",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("conversation lacks %q:\n%s", want, all)
		}
	}
}
