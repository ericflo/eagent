package relay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pickerHost shows Claude's prompt until a command that opens a picker is
// pasted; from then on it shows the picker, as Claude Code does for /model.
type pickerHost struct {
	fakeHost
	opens, picker string
}

func (h *pickerHost) Paste(target, text string) error {
	_ = h.fakeHost.Paste(target, text)
	h.mu.Lock()
	if strings.TrimSpace(text) == h.opens {
		h.screen = h.picker
	}
	h.mu.Unlock()
	return nil
}

// A held "/model" then a held message, for Claude: the seat types "/model"
// on its own and Claude opens its model picker. The message waits while the
// picker is up; it is never pasted into it, and no Enter picks a model.
func TestAHeldMessageAfterAHeldPickerCommandIsNotTypedIntoThePicker(t *testing.T) {
	saved, savedSettle := heldGrace, typeSettle
	heldGrace, typeSettle = 20*time.Millisecond, time.Millisecond
	t.Cleanup(func() { heldGrace, typeSettle = saved, savedSettle })
	spool := filepath.Join(t.TempDir(), "after_turn")
	prompt := "> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	picker := " Select model\n ❯ 1. Default (recommended)\n   2. Opus\n   3. Haiku\n\n Enter to confirm · Esc to exit"
	h := &pickerHost{fakeHost: fakeHost{alive: true, screen: prompt}, opens: "/model", picker: picker}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("c1", "/model", "after_turn"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond) // spool names are ordered by time
	if _, err := s.Send("m1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	// The turn ends; the Stop hook leaves both (the command is first).
	var out strings.Builder
	RunStopHook(spool, strings.NewReader("{}"), &out)
	s.mu.Lock()
	s.state, s.stateAt = "idle", time.Now().Add(-time.Second)
	s.mu.Unlock()
	t0 := time.Now()
	s.releaseHeld(t0)
	s.releaseHeld(t0.Add(10 * heldGrace))
	h.mu.Lock()
	typed := append([]string(nil), h.typed...)
	screen := h.screen
	h.mu.Unlock()
	if screen == picker && len(typed) > 1 {
		t.Fatalf("the held message was pasted (and Enter pressed) into Claude's model picker: typed %q", typed)
	}
}

// A held message that starts with a path (the user wrote
// "/home/... has the plan") is a message: Claude's Stop hook hands it over
// at the turn's end like any other.
func TestTheStopHookHandsOverAHeldMessageThatStartsWithAPath(t *testing.T) {
	t.Parallel()
	spool := t.TempDir()
	msg := "/home/eric/notes/plan.md has the plan for the parser; follow it step by step"
	if err := spoolMessage(spool, "m1", msg); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	RunStopHook(spool, strings.NewReader("{}"), &out)
	if !strings.Contains(out.String(), "plan.md has the plan") {
		t.Fatalf("the Stop hook did not hand the held message over (said %q)", out.String())
	}
}

// The same message held for an agent with its own queue (agy) is tracked
// and confirmed when the transcript shows it, like any message.
func TestAnAfterTurnMessageThatStartsWithAPathIsStillTracked(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "antigravity"}, antigravityAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("m1", "/tmp/build.log shows the failure; fix it", "after_turn"); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Pending) != 1 {
		t.Fatalf("an after-turn message starting with a path is not tracked; pending %v", s.Snapshot().Pending)
	}
}

const claudePromptScreen = "> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)"

// A held message that never finds the agent at its prompt (the picker
// stays up) is reported undelivered after heldGiveUp, with the screen, and
// taken out of the spool, so it is neither kept for ever nor typed later.
func TestAHeldMessageThatNeverFindsThePromptIsReported(t *testing.T) {
	saved, savedGive := heldGrace, heldGiveUp
	heldGrace, heldGiveUp = 0, 100*time.Millisecond
	t.Cleanup(func() { heldGrace, heldGiveUp = saved, savedGive })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: " Select model\n ❯ 1. Default\n\n Enter to confirm · Esc to exit"}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("m1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.state = "idle"
	t0 := time.Now()
	s.releaseHeld(t0)
	s.releaseHeld(t0.Add(heldGiveUp / 2))
	if evs := drain(s.events); len(evs) != 0 {
		t.Fatalf("reported before heldGiveUp: %+v", evs)
	}
	s.releaseHeld(t0.Add(heldGiveUp))
	var lost []Event
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && !ev.OK && ev.SendID == "m1" {
			lost = append(lost, ev)
		}
	}
	left, _ := os.ReadDir(spool)
	if len(lost) != 1 || !strings.Contains(lost[0].Detail, "Select model") || len(left) != 0 || len(h.typed) != 0 {
		t.Fatalf("lost %+v, spool %v, typed %q", lost, left, h.typed)
	}
	if len(s.Snapshot().Pending) != 0 {
		t.Fatalf("still pending %v", s.Snapshot().Pending)
	}
}

// A stuck send's Enter is pressed again only where it sits in the agent's
// input box, not while the agent is busy or a dialog it reported is up.
func TestAStuckSendIsNotPressedWhileTheAgentIsBusy(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true, screen: "> fix the tests\n✻ Compacting… (esc to interrupt)\n  ⏵⏵ bypass permissions on"}
	s := newSeat(Spec{Name: "claude"}, claudeAgent{}, h, "x", "")
	s.state = "idle"
	s.track("r1", "fix the tests", "queue")
	s.checkScreen(time.Now().Add(7 * time.Second))
	if len(h.keys) != 0 {
		t.Fatalf("pressed %q while the agent was busy", h.keys)
	}
}

// The agent's own command is a "/" and a name (with a namespace, a dash or
// underscores, as plugins, skills and MCP prompts have), then a space or
// the end. A path is not one.
func TestAgentCommandsAreToldFromPaths(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]bool{
		"/model":                      true,
		"/compact keep the plan":      true,
		"/plugin-name:do-thing args":  true,
		"/mcp__github__list_prs":      true,
		"/frontend-design":            true,
		"/new":                        true,
		"  /goal finish the parser":   true,
		"/home/eric/plan.md has it":   false,
		"/tmp/build.log shows it":     false,
		"//comment":                   false,
		"/ spaced":                    false,
		"/2fa codes are in the vault": false,
		"fix /model handling":         false,
	} {
		if got := isAgentCommand(text); got != want {
			t.Errorf("isAgentCommand(%q) = %v", text, got)
		}
	}
}

// The agent's own command typed in is confirmed when typed: it runs, and no
// message arrives to wait for.
func TestATypedCommandIsConfirmed(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "codex"}, codexAgent{}, h, "x", "")
	s.state = "idle"
	if _, err := s.Send("c1", "/compact", "queue"); err != nil {
		t.Fatal(err)
	}
	var ok bool
	for _, ev := range drain(s.events) {
		ok = ok || ev.Kind == KindDelivered && ev.OK && ev.SendID == "c1"
	}
	if !ok || len(s.Snapshot().Pending) != 0 {
		t.Fatalf("typed command not confirmed; pending %v", s.Snapshot().Pending)
	}
}

// A working pi whose status file says a dialog is up is not at its own
// screen: nothing is typed into the dialog.
func TestAWorkingPiWithADialogIsNotAtItsOwnScreen(t *testing.T) {
	t.Parallel()
	status := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(status, []byte(`{"state":"working","prompt":"Allow rm -rf build?"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSeat(Spec{Name: "pi", StatusFile: status}, piAgent{label: "Pi"}, &fakeHost{alive: true, screen: piFooterScreen}, "x", "")
	if (piAgent{}).reportedWorking(s) {
		t.Fatal("a pi with a dialog up reads as working at its own screen")
	}
}
