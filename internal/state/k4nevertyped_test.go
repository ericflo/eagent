package state

import (
	"slices"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// NeverTyped: a send held for a start is never typed while the record has
// neither that start, nor its passing on, nor an outcome for it.
func TestK4NeverTyped(t *testing.T) {
	send := func(name, id string, held bool) event.Event {
		return event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: name, ID: id, From: "user", Mode: "queue", Text: "x", Held: held})
	}
	start := func(name string) event.Event {
		return event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: name})
	}
	moved := func(to, id, prev string) event.Event {
		return event.New(event.RelayMoved, event.ActorHarness, event.RelayMovedData{RelaySendData: event.RelaySendData{Name: to, ID: id}, Prev: prev})
	}
	done := func(name, id string) event.Event {
		return event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: name, ID: id, OK: false, Outcome: event.DeliveryNotSent})
	}
	end := func(name string) event.Event {
		return event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: name})
	}
	for _, c := range []struct {
		name string
		evs  []event.Event
		want []string
	}{
		{"sent to an agent that is up", []event.Event{start("codex"), send("codex", "r1", false)}, nil},
		{"held, and the start never recorded", []event.Event{start("codex"), send("codex", "r1", true)}, []string{"r1"}},
		{"held, then the agent stopped, no start", []event.Event{start("codex"), send("codex", "r1", true), end("codex")}, []string{"r1"}},
		{"held for a start that is on the record", []event.Event{start("codex"), send("codex", "r1", true), start("codex")}, nil},
		{"held under a name another agent's start does not free", []event.Event{start("codex"), send("codex", "r1", true), start("claude")}, []string{"r1"}},
		{"held for a switch that passed it on", []event.Event{start("codex"), send("codex", "r1", true), end("codex"), start("claude"), moved("claude", "r1", "codex")}, nil},
		{"held, and the start failed", []event.Event{start("codex"), send("codex", "r1", true), done("codex", "r1")}, nil},
		{"two held, in the order sent, one before a start", []event.Event{start("codex"), send("codex", "r1", true), start("codex"), send("codex", "r2", true), send("codex", "r3", true)}, []string{"r2", "r3"}},
	} {
		var got []string
		for _, d := range NeverTyped(c.evs) {
			got = append(got, d.ID)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
