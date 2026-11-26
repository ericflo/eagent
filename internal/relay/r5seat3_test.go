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
// one transcript record (the Stop hook, pi's extension and releaseHeld all
// join them with a blank line). Both arrived, so both are confirmed.
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

// A held "/compact" and a held message: the command is typed on its own, so
// Claude runs it, and the message after it is typed as a message, never as
// the command's arguments, and is not reported delivered before it arrives.
func TestAHeldCommandDoesNotSwallowAHeldMessage(t *testing.T) {
	saved, savedSettle := heldGrace, typeSettle
	heldGrace, typeSettle = 0, 10*time.Millisecond
	t.Cleanup(func() { heldGrace, typeSettle = saved, savedSettle })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: "> \n  ⏵⏵ bypass permissions on"}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("c1", "/compact", "after_turn"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond) // spool names are ordered by time
	if _, err := s.Send("m1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.fold(obs{kind: "aborted"})
	s.releaseHeld(time.Now().Add(time.Second))
	h.mu.Lock()
	typed := append([]string(nil), h.typed...)
	h.mu.Unlock()
	var okIDs []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && ev.OK {
			okIDs = append(okIDs, ev.SendID)
		}
	}
	for _, id := range okIDs {
		if id == "m1" {
			for _, p := range typed {
				if strings.HasPrefix(p, "/compact") && strings.Contains(p, "then run the tests") {
					t.Fatalf("the message was typed as the command's arguments (%q) and reported delivered (%v)", typed, okIDs)
				}
			}
		}
	}
}

// Held messages left in the spool are typed one at a time, in order, each
// as its own paste after its own look and heldGrace after the one before;
// a command among them is typed on its own like any other.
func TestHeldMessagesLeftInTheSpoolAreTypedOneAtATimeInOrder(t *testing.T) {
	saved, savedSettle := heldGrace, typeSettle
	heldGrace, typeSettle = 50*time.Millisecond, time.Millisecond
	t.Cleanup(func() { heldGrace, typeSettle = saved, savedSettle })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: claudePromptScreen}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	texts := []string{"first", "/compact", "then run the tests", "and report"}
	for _, text := range texts {
		if err := spoolMessage(spool, "m", text); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	s.state, s.stateAt = "idle", time.Now().Add(-time.Second)
	t0 := time.Now()
	typed := func() string {
		h.mu.Lock()
		defer h.mu.Unlock()
		return strings.Join(h.typed, "|")
	}
	for i := range texts {
		want := strings.Join(texts[:i+1], "|")
		at := t0.Add(time.Duration(i) * heldGrace)
		s.releaseHeld(at)
		if got := typed(); got != want {
			t.Fatalf("after release %d typed %q, want %q", i, got, want)
		}
		// Within heldGrace of a release nothing more is typed.
		s.releaseHeld(at.Add(heldGrace / 2))
		if got := typed(); got != want {
			t.Fatalf("typed %q within heldGrace of the last release", got)
		}
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

// An after-turn text handed to an agent's own queue (typed into agy) is
// watched like any message, a command too, as at 80f4746.
func TestAnAfterTurnCommandInAnAgentsOwnQueueIsTracked(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "antigravity"}, antigravityAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("c1", "/compact", "after_turn"); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Pending) != 1 {
		t.Fatalf("pending %v", s.Snapshot().Pending)
	}
}
