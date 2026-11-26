package harness

import (
	"slices"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// outcomeOf is the one relay.delivered the record holds for a send, or
// nil; it fails when there are two.
func outcomeOf(t *testing.T, evs []event.Event, id string) *event.RelayDeliveredData {
	t.Helper()
	var out *event.RelayDeliveredData
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id {
			if out != nil {
				t.Fatalf("%s has two outcomes on the record", id)
			}
			out = &d
		}
	}
	return out
}

// A message typed into an agent that is then restarted, before its arrival
// showed, has nobody to confirm it: the process is gone and the new start
// waits on nothing. It is reported unconfirmed once, so eagent can deal
// with it, and never typed again. One held for the start is typed by the
// new agent, once, and is not reported.
func TestASendUnconfirmedWhenItsAgentIsRestartedIsReportedOnce(t *testing.T) {
	old, next := newFakeSeat("one"), newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	var typed string
	rt.sync(func() {
		typed = rt.st.NextRelaySendID()
		rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: typed, From: "user", Mode: "queue", Text: "tidy the shelves"}))
	})
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	userSays(t, rt, "also sweep the floor")
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	waitFor(t, "the held message in the new agent", func() bool {
		return slices.Contains(next.sentTexts(), "also sweep the floor")
	})
	var evs []event.Event
	var outcome state.SendOutcome
	rt.sync(func() { evs, outcome = slices.Clone(rt.st.Events), rt.st.SendOutcome(typed) })
	d := outcomeOf(t, evs, typed)
	if d == nil || d.OK || d.Outcome != event.DeliveryUnconfirmed || !strings.Contains(d.Detail, "was restarted") {
		t.Fatalf("the send typed into the restarted agent: %+v, want one unconfirmed that says it was restarted", d)
	}
	if outcome != state.SendUnconfirmed {
		t.Errorf("its outcome reads %q", outcome)
	}
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID != typed && !d.OK {
			t.Errorf("the message held for the start was reported: %+v", d)
		}
	}
	if got := next.sentTexts(); slices.Contains(got, "tidy the shelves") || len(got) != 1 {
		t.Fatalf("the new agent was typed %q, want only the held message", got)
	}
	if got := old.sentTexts(); len(got) != 0 {
		t.Fatalf("typed into the stopped agent: %q", got)
	}
}

// The same when the agent is stopped: a message it was typed and never
// shown to have is reported unconfirmed once, with the reason.
func TestASendUnconfirmedWhenItsAgentIsStoppedIsReportedOnce(t *testing.T) {
	seat := newFakeSeat("one")
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	var typed string
	rt.sync(func() {
		typed = rt.st.NextRelaySendID()
		rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: typed, From: "user", Mode: "queue", Text: "tidy the shelves"}))
	})
	args := map[string]any{"action": "stop"}
	if text, isErr := rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false); isErr {
		t.Fatalf("stop: %s", text)
	}
	var evs []event.Event
	var waiting bool
	rt.sync(func() { evs, waiting = slices.Clone(rt.st.Events), rt.st.Waiting() })
	d := outcomeOf(t, evs, typed)
	if d == nil || d.OK || d.Outcome != event.DeliveryUnconfirmed || !strings.Contains(d.Detail, "was stopped") {
		t.Fatalf("the send typed into the stopped agent: %+v, want one unconfirmed that says it was stopped", d)
	}
	if !waiting {
		t.Errorf("the session still waits on a stopped agent")
	}
	if got := seat.sentTexts(); len(got) != 0 {
		t.Fatalf("typed into the stopped agent: %q", got)
	}
}
