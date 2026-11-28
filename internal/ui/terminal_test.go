package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/harness"
)

// testTerminal writes what eagent says to a buffer and the rest to a file.
func testTerminal(t *testing.T, verbose bool) (*Terminal, *bytes.Buffer, func() string) {
	t.Helper()
	errFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	term := &Terminal{out: out, err: errFile, verbose: verbose, who: map[string]string{}, sends: map[string]bool{}, stopSpin: make(chan struct{})}
	return term, out, func() string {
		raw, _ := os.ReadFile(errFile.Name())
		return string(raw)
	}
}

func TestStatusLineShowsEagentAndEachAgent(t *testing.T) {
	term, _, _ := testTerminal(t, false)
	term.status = harness.Status{Busy: true, BusyFor: "3s", ContextTokens: 12000, Phone: true, Agents: []harness.AgentStatus{{Name: "claude", Who: "Claude", State: "working"}}}
	term.Stream("tool", "screen")
	if got := term.statusText(); got != "eagent calling screen 3s · Claude working · ctx 12k · phone" {
		t.Fatalf("status = %q", got)
	}
	term.status = harness.Status{Waiting: true, Agents: []harness.AgentStatus{{Name: "claude", Who: "Claude", State: "idle"}}}
	if got := term.statusText(); got != "" {
		t.Fatalf("a waiting session shows %q", got)
	}
}

func TestAgentTurnAndDeliveryReachTheUser(t *testing.T) {
	term, out, errText := testTerminal(t, false)
	term.Trace(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "codex", Agent: "codex", AgentLabel: "Codex", Dir: "/w"}))
	term.Trace(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: "r1", From: "user", Text: "hi"}))
	term.Trace(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: "r2", From: "eagent", Text: "keep going"}))
	term.Trace(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: true}))
	term.Trace(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "codex", ID: "r2", OK: true}))
	term.Trace(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "codex", Text: "Fixed the test.\nAll green.", Tools: 3}))
	if s := out.String(); !strings.Contains(s, "Codex finished a turn (3 tool calls)") || !strings.Contains(s, "┃ All green.") {
		t.Fatalf("stdout = %q", s)
	}
	if s := errText(); strings.Count(s, "delivered to Codex") != 1 || !strings.Contains(s, "Codex started in /w") {
		t.Fatalf("stderr = %q", s)
	}
}

// A message of the user's on its way to Claude is passed on to Codex by a
// switch, and arrives there: the receipt names Codex, where it arrived
// (it named Claude, which was stopped). And an arrival that shows after
// the message was first called not confirmed is printed too.
func TestADeliveryReceiptNamesTheAgentItArrivedIn(t *testing.T) {
	term, _, errText := testTerminal(t, false)
	term.Trace(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Agent: "claude", AgentLabel: "Claude", Dir: "/w"}))
	term.Trace(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Text: "hi"}))
	term.Trace(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "codex", Agent: "codex", AgentLabel: "Codex", Dir: "/w"}))
	term.Trace(event.New(event.RelayMoved, event.ActorHarness, event.RelayMovedData{RelaySendData: event.RelaySendData{Name: "codex", ID: "r1", From: "user", Text: "hi"}, Prev: "claude"}))
	term.Trace(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: false, Outcome: event.DeliveryUnconfirmed, Detail: "its transcript never showed it"}))
	term.Trace(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: true}))
	if s := errText(); !strings.Contains(s, "not confirmed by Codex") || !strings.Contains(s, "delivered to Codex") || strings.Contains(s, "by Claude") || strings.Contains(s, "to Claude") {
		t.Fatalf("stderr = %q", s)
	}
}

// An old log's events print a plain line in verbose mode and nothing
// otherwise; schedules print either way.
func TestLegacyEventsPrintPlainly(t *testing.T) {
	term, _, _ := testTerminal(t, true)
	ev := event.New(event.LegacyTaskCreate, event.LegacyOrchestrator, map[string]any{"id": "t1", "title": "Read the code"})
	if got := term.traceLine(ev); got != "orchestrator task.create: Read the code" {
		t.Fatalf("legacy line = %q", got)
	}
	term.verbose = false
	if got := term.traceLine(ev); got != "" {
		t.Fatalf("legacy line without -v = %q", got)
	}
	fire := event.New(event.ScheduleFire, event.LegacyOrchestrator, map[string]any{"id": "s1"})
	if got := term.traceLine(fire); got != "⏰ s1 fired" {
		t.Fatalf("schedule line = %q", got)
	}
}
