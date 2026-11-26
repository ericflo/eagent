package relay

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Claude is idle at its prompt; its last answer, still on screen, quotes
// the words of its own busy line (it was working on this very code). The
// held message left in the spool is typed: the busy line counts only where
// Claude draws it, by its input box.
func TestClaudeHeldMessageIsTypedAtAPromptWhoseOutputQuotesTheBusyLine(t *testing.T) {
	saved, savedGive, savedSettle := heldGrace, heldGiveUp, typeSettle
	heldGrace, heldGiveUp, typeSettle = 0, 100*time.Millisecond, time.Millisecond
	t.Cleanup(func() { heldGrace, heldGiveUp, typeSettle = saved, savedGive, savedSettle })
	spool := filepath.Join(t.TempDir(), "after_turn")
	screen := "● Done. busy() now reports Claude working while its spinner line\n" +
		"  says \"esc to interrupt\", so the seat leaves a quiet agent blocked.\n\n" +
		"> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	h := &fakeHost{alive: true, screen: screen}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("m1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.state, s.stateAt = "idle", time.Now().Add(-time.Second)
	t0 := time.Now()
	s.releaseHeld(t0)
	s.releaseHeld(t0.Add(heldGiveUp))
	var lost []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && !ev.OK {
			lost = append(lost, ev.SendID)
		}
	}
	h.mu.Lock()
	typed := append([]string(nil), h.typed...)
	h.mu.Unlock()
	if len(typed) == 0 {
		t.Fatalf("Claude sat at its prompt and the held message was never typed; reported lost: %v", lost)
	}
}

// An Antigravity relay line whose state name is not "idle" (here empty, as
// before agy reports an agent_state; antigravity.go says the names are not
// documented) reads as busy; the stuck send's Enter is pressed again all
// the same, as at 80f4746: busy-ness does not gate it.
func TestAgyStuckSendIsPressedAgainWithAnUnknownStateName(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true, screen: "> fix the tests\n\nAntigravity relay ·  · Gemini 3 Pro"}
	s := newSeat(Spec{Name: "antigravity"}, antigravityAgent{}, h, "x", "")
	s.state, s.stateAt = "idle", time.Now().Add(-time.Minute)
	s.track("r1", "fix the tests", "queue")
	s.checkScreen(time.Now().Add(7 * time.Second))
	if len(h.keys) == 0 {
		t.Fatalf("the stuck send sat in agy's input box and Enter was not pressed again (pending %v)", s.Snapshot().Pending)
	}
}

// A held message whose first word is a directory ("/tmp is full ...") is
// handed over by the Stop hook, as every held message is.
func TestAHeldMessageStartingWithADirectoryIsHandedOver(t *testing.T) {
	t.Parallel()
	spool := t.TempDir()
	msg := "/tmp is full again; clear the old build outputs before the next run"
	if err := spoolMessage(spool, "m1", msg); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	RunStopHook(spool, strings.NewReader("{}"), &out)
	if !strings.Contains(out.String(), "clear the old build outputs") {
		t.Errorf("the Stop hook did not hand the held message over (said %q); isAgentCommand=%v", out.String(), isAgentCommand(msg))
	}
}
