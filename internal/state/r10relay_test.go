package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Finding 7. A send held for a new agent under the name of one that was
// stopped is the new agent's: the record names it as the holder at the
// send's seq, and counts the send as its own (how many, from whom, when).
func TestR10ASendHeldForANewAgentIsItsOwnFromItsSeq(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude", Reason: "stopped"}))
	send := b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Mode: "queue", Text: "build the payment webhook handler"}))
	r9start(b, false)
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: "r1", OK: true}))
	st := Replay(b.events)
	rel := st.Relays["claude"]
	if at := st.RelayAt("claude", send.Seq); at != rel {
		t.Errorf("the send's holder is the stopped agent (start %d), want the new one (start %d)", at.StartSeq, rel.StartSeq)
	}
	if rel.Sends != 1 || rel.LastSendFrom != "user" || !rel.LastSendAt.Equal(send.Time) {
		t.Errorf("the new agent's sends: %d, last from %q at %v; want 1, from user at %v", rel.Sends, rel.LastSendFrom, rel.LastSendAt, send.Time)
	}
}

// Finding 7, its bound. A stopped relay restarted on its own conversation
// is the same relay: it holds the name from its first start, and a send
// held for the restart is counted once.
func TestR10ASendHeldForARestartOfAStoppedRelayIsCountedOnce(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	first := b.events[len(b.events)-1].Seq
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude", Reason: "stopped"}))
	r9send(b, "r1", "held for the restart")
	r9start(b, true)
	rel := Replay(b.events).Relays["claude"]
	if rel.StartSeq != first || rel.Sends != 1 || len(rel.Undelivered) != 1 {
		t.Errorf("start %d (want %d), %d sends (want 1), waiting on %v", rel.StartSeq, first, rel.Sends, rel.Undelivered)
	}
}
