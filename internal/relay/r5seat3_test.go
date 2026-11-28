package relay

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Two held messages that share a long start are handed over together, in
// one transcript record (the Stop hook and pi's extension both join them
// with a blank line). Both arrived, so both are confirmed.
func TestJoinedHeldMessagesWithACommonStartAreBothConfirmed(t *testing.T) {
	t.Parallel()
	intro := strings.Repeat("CI failed on main. Read the log below, find the cause and fix it. ", 3)
	a := intro + "\nFAIL internal/relay TestOne"
	b := intro + "\nFAIL internal/harness TestTwo"
	for name, got := range map[string]string{
		"a-then-b": a + "\n\n" + b,
		"b-then-a": b + "\n\n" + a,
	} {
		s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
		s.track("r1", a, "after_turn")
		s.track("r2", b, "after_turn")
		if ids := s.matchPending(got); len(ids) != 2 {
			t.Errorf("%s: one record holding both messages confirmed %v, want r1 and r2", name, ids)
		}
	}
}

// A pi that went quiet in a long tool call (its status file says working,
// its screen shows "Working..." and its footer) is blocked as hung. A
// message to it, and an interrupt above all, is still typed: pi queues
// what is typed while it works, and nothing on screen waits for an answer:
// its own status says it works, and it reports its dialogs itself.
func TestAQuietPiInALongToolCallStillTakesMessages(t *testing.T) {
	status := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(status, []byte(`{"state":"working"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	saved, savedSettle := interruptPause, typeSettle
	interruptPause, typeSettle = 10*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { interruptPause, typeSettle = saved, savedSettle })
	for _, mode := range []string{"queue", "interrupt", "after_turn"} {
		h := &fakeHost{alive: true, screen: piFooterScreen}
		s := newSeat(Spec{Name: "pi", StatusFile: status}, piAgent{label: "Pi"}, h, "x", "")
		start := time.Now()
		s.state, s.stateAt, s.lastLine = "working", start.Add(-11*time.Minute), start.Add(-11*time.Minute)
		s.checkScreen(start)
		if st := s.Snapshot().State; st != "blocked" {
			t.Fatalf("seat is %s, want blocked (hung)", st)
		}
		s.spec.Spool = filepath.Join(t.TempDir(), "after_turn")
		msg, err := s.Send("r1", "stop and tell me where you are", mode)
		held, _ := filepath.Glob(filepath.Join(s.spec.Spool, "*.txt"))
		switch {
		case err != nil:
			t.Errorf("%s send to a quiet working pi refused: %v (typed %q)", mode, err, h.typed)
		case mode == "after_turn" && len(held) != 1:
			t.Errorf("%s send: %q, held %v", mode, msg, held)
		case mode != "after_turn" && len(h.typed) != 1:
			t.Errorf("%s send: %q, typed %q", mode, msg, h.typed)
		case mode == "interrupt" && !slices.Contains(h.keys, "Escape"):
			t.Errorf("interrupt pressed %q", h.keys)
		}
	}
}

// A "/compact" for the end of the turn is refused (it would be handed over
// as text), and a held message left over after an interrupt is reported
// undelivered; neither is typed, so the message is never taken for the
// command's arguments.
func TestLeftOverHeldCommandAndMessageAreReportedNotTyped(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: "> \n  ⏵⏵ bypass permissions on"}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("c1", "/compact", "after_turn"); err == nil {
		t.Fatal("the command was held")
	}
	if _, err := s.Send("m1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.fold(obs{kind: "aborted"})
	s.reportLeftOvers(time.Now().Add(time.Second))
	var lost []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && !ev.OK {
			lost = append(lost, ev.SendID)
		}
	}
	if strings.Join(lost, ",") != "m1" || len(h.typed) != 0 {
		t.Fatalf("lost %v, typed %q", lost, h.typed)
	}
}

// Every left-over is reported, in the order it was held.
func TestLeftOverHeldMessagesAreReportedInOrder(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	for _, id := range []string{"a1", "a2", "a3", "a4"} {
		if err := spoolMessage(spool, id, "text of "+id); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	s.state = "working"
	s.fold(obs{kind: "turn"})
	drain(s.events)
	s.reportLeftOvers(time.Now().Add(time.Second))
	var lost []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && !ev.OK {
			lost = append(lost, ev.SendID)
		}
	}
	if strings.Join(lost, ",") != "a1,a2,a3,a4" || len(h.typed) != 0 {
		t.Fatalf("lost %v, typed %q", lost, h.typed)
	}
}

// Claude's Stop hook hands over every held message, in order, as text, a
// held command among them, as at 80f4746.
func TestTheStopHookHandsOverEveryHeldMessageAsText(t *testing.T) {
	t.Parallel()
	spool := t.TempDir()
	for _, text := range []string{"first", "/compact", "after"} {
		if err := spoolMessage(spool, "m", text); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	var out strings.Builder
	RunStopHook(spool, strings.NewReader("{}"), &out)
	if !strings.Contains(out.String(), `"reason":"first\n\n/compact\n\nafter"`) {
		t.Fatalf("hook said %q", out.String())
	}
	if left, _ := filepath.Glob(filepath.Join(spool, "*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

// escHost is a screen that shows the agent's prompt once Escape is pressed
// (when clears is set).
type escHost struct {
	fakeHost
	clears string
}

func (h *escHost) Keys(target string, keys ...string) error {
	_ = h.fakeHost.Keys(target, keys...)
	h.mu.Lock()
	if h.clears != "" && slices.Contains(keys, "Escape") {
		h.screen = h.clears
	}
	h.mu.Unlock()
	return nil
}

// An interrupt to a quiet seat showing a dialog the seat does not know gets
// through: Escape is pressed. Its text is typed only if the agent's prompt
// shows afterwards, never into the dialog.
func TestAnInterruptReachesAQuietSeatButTypesOnlyAtThePrompt(t *testing.T) {
	saved, savedSettle := interruptPause, typeSettle
	interruptPause, typeSettle = 10*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { interruptPause, typeSettle = saved, savedSettle })
	dialog := " Which approach should I take?\n\n ❯ 1. Rewrite the parser\n   2. Patch the caller\n\n Enter to select · Esc to cancel"
	for _, c := range []struct {
		name, clears string
		typed        bool
	}{
		{"dialog stays", "", false},
		{"prompt comes back", "> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)", true},
	} {
		h := &escHost{fakeHost: fakeHost{alive: true, screen: dialog}, clears: c.clears}
		s := newSeat(Spec{Name: "claude"}, claudeAgent{}, h, "x", "")
		s.state, s.hung = "blocked", true
		_, err := s.Send("r1", "stop", "interrupt")
		if !slices.Contains(h.keys, "Escape") {
			t.Fatalf("%s: no Escape (keys %q, err %v)", c.name, h.keys, err)
		}
		if got := len(h.typed) == 1; got != c.typed || (err == nil) != c.typed {
			t.Fatalf("%s: typed %q, err %v", c.name, h.typed, err)
		}
		if !c.typed && slices.Contains(h.keys, "Enter") {
			t.Fatalf("%s: Enter pressed into the dialog", c.name)
		}
	}
}

// An after-turn message handed to an agent's own queue (typed into agy) is
// watched like any message, as at 80f4746. (The agent's own command shows
// no arrival: it is delivered once handed over, see
// TestR10AnAfterTurnCommandHandedToAnAgentsQueueIsDelivered.)
func TestAnAfterTurnCommandInAnAgentsOwnQueueIsTracked(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "antigravity"}, antigravityAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("c1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Pending) != 1 {
		t.Fatalf("pending %v", s.Snapshot().Pending)
	}
}
