package state

import (
	"maps"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

func r9send(b *builder, id, text string) {
	b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: id, From: "user", Mode: "queue", Text: text}))
}

func r9start(b *builder, resumed bool) {
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", Dir: "/p", Resumed: resumed}))
}

func r9waiting(t *testing.T, b *builder, want map[string]string) {
	t.Helper()
	rel := Replay(b.events).Relays["claude"]
	if !maps.Equal(rel.Undelivered, want) {
		t.Fatalf("waiting on %v, want %v", rel.Undelivered, want)
	}
	if rel.Awaited() != len(want) {
		t.Fatalf("awaited %d, want %d", rel.Awaited(), len(want))
	}
}

// A send recorded while a restart was under way (held for it, as the
// harness records it before the restart's relay.start) is still the
// relay's after the start: the record waits on it until its outcome.
// What the old agent had been typed is settled before the start.
func TestR9ASendHeldForARestartStaysWaitedOn(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		b := newBuilder()
		b.startClaude()
		r9send(b, "r1", "check the version")
		b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: "r1", OK: false, Outcome: event.DeliveryUnconfirmed, Detail: "the agent was restarted"}))
		r9send(b, "r2", "also sweep the floor")
		r9start(b, resumed)
		r9waiting(t, b, map[string]string{"r2": "also sweep the floor"})
	}
}

// A new agent under the name of one that was stopped takes the sends held
// for its start, and none of what the stopped agent was typed: those stay
// with that agent.
func TestR9ANewAgentUnderAStoppedNameTakesOnlyTheSendsHeldForIt(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	r9send(b, "r1", "typed into the agent that was stopped")
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude", Reason: "stopped"}))
	r9send(b, "r2", "held for the new start")
	r9start(b, false)
	r9waiting(t, b, map[string]string{"r2": "held for the new start"})
}

// A send held for a start that the session's end cut short (recorded
// before a resume) was not held for a start in a later run.
func TestR9ASendHeldInAnEarlierRunIsNotTakenByALaterStart(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude", Reason: "stopped"}))
	r9send(b, "r1", "held for a start that never came up")
	b.add(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{}))
	r9start(b, false)
	r9waiting(t, b, map[string]string{})
}
