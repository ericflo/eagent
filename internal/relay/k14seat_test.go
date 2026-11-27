package relay

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An agent's own command sent for the end of the turn of a working Claude
// or pi would go into the spool, and the hand-over gives a spool's text to
// the agent's model: Claude's Stop hook as the reason of a "block" decision
// ("Stop hook feedback:\n/compact"), pi's extension as a user message. The
// command would never run, and the seat would confirm it from that text.
// It is refused with the reason: nothing is held, typed or waited on, and
// the hook has nothing to hand over. (Before the twelfth self-review pass
// it was held and confirmed as a held message is; main does that too.)
func TestK14ACommandForTheEndOfAWorkingTurnIsRefusedWhereItWouldArriveAsText(t *testing.T) {
	for _, agent := range []Agent{claudeAgent{}, piAgent{}} {
		spool := filepath.Join(t.TempDir(), "after_turn")
		h := &fakeHost{alive: true, screen: "✻ Thinking… (12s · esc to interrupt)\n" + claudePromptScreen}
		s := newSeat(Spec{Spool: spool, Hook: "eagent relay-hook " + spool}, agent, h, "k14", "")
		s.state = "working"
		got, err := s.Send("r1", "/compact now", "after_turn")
		if err == nil || !strings.Contains(err.Error(), "/compact was not sent") || !strings.Contains(err.Error(), "Send it once the turn has ended") {
			t.Fatalf("%T: send said %q, %v; want a refusal with the reason", agent, got, err)
		}
		h.mu.Lock()
		typed := len(h.typed) + len(h.keys)
		h.mu.Unlock()
		files, _ := os.ReadDir(spool)
		var out bytes.Buffer
		RunStopHook(spool, strings.NewReader(`{"hook_event_name":"Stop"}`), &out)
		if typed != 0 || len(files) != 0 || len(s.Snapshot().Pending) != 0 || strings.Contains(out.String(), "/compact") {
			t.Fatalf("%T: typed or pressed %d, spool %v, pending %v, hook said %q", agent, typed, files, s.Snapshot().Pending, out.String())
		}
		if ev := deliveries(drain(s.events)); len(ev) != 0 {
			t.Fatalf("%T: reported %+v for a send that was refused", agent, ev)
		}
		// A message for the end of the turn is still held.
		if msg, err := s.Send("r2", "then run the tests", "after_turn"); err != nil || !strings.Contains(msg, "held for") {
			t.Fatalf("%T: a message: %q %v", agent, msg, err)
		}
	}
}

// The agent at rest: the command is typed into its prompt, where it runs,
// whatever the mode.
func TestK14ACommandForAnIdleClaudeIsTyped(t *testing.T) {
	saved := typeSettle
	typeSettle = time.Millisecond
	t.Cleanup(func() { typeSettle = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: claudePromptScreen}
	s := newSeat(Spec{Spool: spool, Hook: "eagent relay-hook " + spool}, claudeAgent{}, h, "k14", "")
	s.state = "idle"
	if _, err := s.Send("r1", "/compact", "after_turn"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	typed := strings.Join(h.typed, "|")
	h.mu.Unlock()
	if typed != "/compact" {
		t.Fatalf("typed %q", typed)
	}
}
