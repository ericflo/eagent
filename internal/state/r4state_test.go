package state

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// reviewByCodex is a log in which a custom relay called "review", backed by
// Codex, did everything a relay can show in eagent's conversation, and was
// then stopped.
func reviewByCodex() *builder {
	b := newBuilder()
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "review", Profile: "review", Agent: "codex", AgentLabel: "Codex", Label: "Codex CLI", Dir: "/p", Host: "tmux", Target: "eagent-1-review"}))
	b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "review", ID: "r1", From: "user", Mode: "queue", Text: "run the tests", Auto: true}))
	b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "review", ID: "r2", From: "eagent", Mode: "after_turn", Text: "and the linter", Auto: true}))
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "review", ID: "r2", OK: false, Detail: "no sign of it"}))
	b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "review", Text: "Running go test"}))
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "review", State: "blocked", Detail: "Allow? [y/n]"}))
	b.add(event.New(event.RelayLimit, event.ActorHarness, event.RelayLimitData{Name: "review", UsedPercent: 40, Window: "5h"}))
	b.add(event.New(event.RelayAutopilot, event.ActorHarness, event.RelayAutopilotData{Name: "review", Instruction: "keep going", Decide: true}))
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "review", Text: "The unit tests passed."}))
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "review", Reason: "the user stopped it"}))
	return b
}

// A name can be taken again by a different agent. What was rendered from
// the events of the first one must not change when the second one starts:
// every line names the agent that held the name when it was written, and
// the conversation already sent to the provider stays byte for byte.
func TestReusingARelayNameDoesNotRewriteWhoWroteEarlierLines(t *testing.T) {
	b := reviewByCodex()
	before := strings.TrimSuffix(flat(Replay(b.events).View(nil)), "\x01")
	if !strings.Contains(before, "<Codex> The unit tests passed.") {
		t.Fatalf("the report is not under Codex's name:\n%s", before)
	}
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "review", Profile: "review", Agent: "pi", AgentLabel: "Pi", Label: "pi", Dir: "/q", Host: "tmux", Target: "eagent-1-review"}))
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "review", Text: "Looked again."}))
	st := Replay(b.events)
	after := flat(st.View(nil))
	if !strings.HasPrefix(after, before) {
		i := 0
		for i < len(before) && i < len(after) && before[i] == after[i] {
			i++
		}
		t.Fatalf("starting pi under the same name changed what was rendered before it, at byte %d:\nbefore: %q\nafter:  %q", i, before[max(0, i-60):min(len(before), i+60)], after[max(0, i-60):min(len(after), i+60)])
	}
	if !strings.Contains(after[len(before):], "<Pi> Looked again.") {
		t.Fatalf("the new agent's report is not under its own name:\n%s", after[len(before):])
	}
	// What the record says about older events follows the same rule.
	if rel := st.RelayAt("review", 3); rel == nil || rel.Agent != "codex" || rel.Dir != "/p" {
		t.Fatalf("RelayAt an early event: %+v", rel)
	}
	if rel := st.RelayAt("review", st.LastSeq()); rel == nil || rel.Agent != "pi" || rel != st.Relays["review"] {
		t.Fatalf("RelayAt the latest event: %+v", rel)
	}
}

// A relay moved between two names (a switch) names the agents that held
// them then, also when one of the names is taken again later.
func TestMovedSendNamesTheAgentsOfItsTime(t *testing.T) {
	b := reviewByCodex()
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", AgentLabel: "Claude", Dir: "/p", Host: "tmux", Target: "eagent-1-claude"}))
	b.add(event.New(event.RelayMoved, event.ActorHarness, event.RelayMovedData{RelaySendData: event.RelaySendData{Name: "claude", ID: "r1", From: "user", Text: "run the tests"}, Prev: "review"}))
	before := strings.TrimSuffix(flat(Replay(b.events).View(nil)), "\x01")
	if !strings.Contains(before, "to Codex waited for the handover; it goes to Claude instead.") {
		t.Fatalf("moved line:\n%s", before)
	}
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "review", Profile: "review", Agent: "pi", AgentLabel: "Pi", Dir: "/q", Host: "tmux", Target: "eagent-1-review"}))
	if after := flat(Replay(b.events).View(nil)); !strings.HasPrefix(after, before) {
		t.Fatalf("the moved line changed after the name was reused:\n%s", after)
	}
}

// A session in which no name was taken by a second agent has one holder
// per name, and RelayAt is the relay itself, for every event.
func TestRelayAtWithoutReuseIsTheRelay(t *testing.T) {
	b := codexWithHistory()
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "codex", Profile: "codex", Agent: "codex", Dir: "/p", Host: "tmux", Target: "eagent-1-codex", InnerID: "conv-1", Resumed: true}))
	st := Replay(b.events)
	for seq := int64(0); seq <= st.LastSeq(); seq++ {
		if got := st.RelayAt("codex", seq); got != st.Relays["codex"] {
			t.Fatalf("seq %d: RelayAt is not the relay", seq)
		}
	}
	if st.RelayAt("nobody", 3) != nil {
		t.Fatal("RelayAt of an unknown name")
	}
}
