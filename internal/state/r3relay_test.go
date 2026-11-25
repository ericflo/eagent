package state

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// codexWithHistory is a log in which a Codex relay found its conversation,
// was sent a message, finished a turn and set autopilot.
func codexWithHistory() *builder {
	b := newBuilder()
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "codex", Profile: "codex", Agent: "codex", Dir: "/p", Host: "tmux", Target: "eagent-1-codex"}))
	b.add(event.New(event.RelayInner, event.ActorHarness, event.RelayInnerData{Name: "codex", InnerID: "conv-1", Transcript: "/t/conv-1.jsonl"}))
	b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: "r1", From: "user", Seq: 3, Mode: "queue", Text: "fix it", Auto: true}))
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: true}))
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "codex", Text: "fixed in abc123"}))
	b.add(event.New(event.RelayLimit, event.ActorHarness, event.RelayLimitData{Name: "codex", UsedPercent: 40, Window: "5h"}))
	return b
}

// An agent started afresh under the name of one that was stopped (a switch
// back to Codex, or a new relay_start) is a new conversation: it does not
// take the stopped one's conversation id, turns or last report.
func TestFreshStartUnderAStoppedRelaysNameIsANewConversation(t *testing.T) {
	b := codexWithHistory()
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "codex", Reason: "handed over to claude"}))
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "codex", Profile: "codex", Agent: "codex", Dir: "/p", Host: "tmux", Target: "eagent-1-codex"}))
	rel := Replay(b.events).Relays["codex"]
	if rel.InnerID != "" || rel.Turns != 0 || rel.LastTurn != "" || !rel.LastTurnAt.IsZero() || rel.Sends != 0 || rel.LastSendFrom != "" {
		t.Fatalf("the new conversation inherited the stopped one's: inner %q, %d turns, last %q, %d sends from %q", rel.InnerID, rel.Turns, rel.LastTurn, rel.Sends, rel.LastSendFrom)
	}
	// The usage budget is the agent's account's, not the conversation's.
	if rel.Limit == nil || rel.Limit.UsedPercent != 40 {
		t.Fatalf("usage limit: %+v", rel.Limit)
	}
	if got := Replay(b.events).RelayOrder; len(got) != 1 {
		t.Fatalf("relay order %q", got)
	}
}

// A restart (a model change, /restart) continues the same relay and keeps
// its history, whether or not the agent's conversation id was known.
func TestRestartKeepsTheRelaysHistory(t *testing.T) {
	for _, resumed := range []bool{true, false} {
		b := codexWithHistory()
		inner := ""
		if resumed {
			inner = "conv-1"
		}
		b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "codex", Profile: "codex", Agent: "codex", Dir: "/p", Host: "tmux", Target: "eagent-1-codex", InnerID: inner, Resumed: resumed, Model: "gpt-6.2-astra"}))
		rel := Replay(b.events).Relays["codex"]
		if rel.InnerID != "conv-1" || rel.Turns != 1 || rel.LastTurn != "fixed in abc123" || rel.Sends != 1 {
			t.Fatalf("resumed=%v: inner %q, %d turns, last %q, %d sends", resumed, rel.InnerID, rel.Turns, rel.LastTurn, rel.Sends)
		}
	}
}

// A stopped relay restarted on its own conversation (relay_control restart
// after a stop) continues it, history and all.
func TestRestartOfAStoppedRelayOnItsConversationKeepsItsHistory(t *testing.T) {
	b := codexWithHistory()
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "codex", Reason: "the user stopped it"}))
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "codex", Profile: "codex", Agent: "codex", Dir: "/p", Host: "tmux", Target: "eagent-1-codex", InnerID: "conv-1", Resumed: true}))
	rel := Replay(b.events).Relays["codex"]
	if rel.InnerID != "conv-1" || rel.Turns != 1 || rel.LastTurn != "fixed in abc123" || !rel.Active() {
		t.Fatalf("inner %q, %d turns, last %q, active %v", rel.InnerID, rel.Turns, rel.LastTurn, rel.Active())
	}
}
