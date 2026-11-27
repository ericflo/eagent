package relay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

// A left-over is reported whatever the screen shows (here a picker left
// open): nothing on that path reads the screen.
func TestALeftOverIsReportedWhateverTheScreenShows(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: " Select model\n ❯ 1. Default\n\n Enter to confirm · Esc to exit"}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("m1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.fold(obs{kind: "turn"})
	s.reportLeftOvers(time.Now().Add(time.Second))
	var lost int
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && !ev.OK && ev.SendID == "m1" {
			lost++
		}
	}
	left, _ := os.ReadDir(spool)
	if lost != 1 || len(left) != 0 || len(h.typed) != 0 || len(s.Snapshot().Pending) != 0 {
		t.Fatalf("lost %d, spool %v, typed %q, pending %v", lost, left, h.typed, s.Snapshot().Pending)
	}
}

// A stuck send's Enter is pressed again whatever the agent's busy line
// shows, as at 80f4746.
func TestAStuckSendIsPressedAgainWhileTheAgentIsBusy(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true, screen: "> fix the tests\n✻ Compacting… (esc to interrupt)\n  ⏵⏵ bypass permissions on"}
	s := newSeat(Spec{Name: "claude"}, claudeAgent{}, h, "x", "")
	s.state = "idle"
	s.track("r1", "fix the tests", "queue")
	s.checkScreen(time.Now().Add(7 * time.Second))
	if strings.Join(h.keys, " ") != "Enter" {
		t.Fatalf("keys %q", h.keys)
	}
}

// No Enter is pressed again while the agent reports a dialog.
func TestAStuckSendIsNotPressedWhileADialogIsReported(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true, screen: "> fix the tests\n  ⏵⏵ bypass permissions on"}
	s := newSeat(Spec{Name: "claude"}, claudeAgent{}, h, "x", "")
	s.state, s.dialogUp = "idle", "Allow rm?"
	s.track("r1", "fix the tests", "queue")
	s.checkScreen(time.Now().Add(7 * time.Second))
	if len(h.keys) != 0 {
		t.Fatalf("keys %q", h.keys)
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

// The agent's own command typed in is confirmed once typed (at the
// watcher's next look): it runs, and no message arrives to wait for.
func TestATypedCommandIsConfirmed(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "codex"}, codexAgent{}, h, "x", "")
	s.state, s.poll = "idle", time.Millisecond
	go s.watch()
	t.Cleanup(s.Close)
	if _, err := s.Send("c1", "/compact", "queue"); err != nil {
		t.Fatal(err)
	}
	var ok bool
	deadline := time.After(5 * time.Second)
	for !ok {
		select {
		case ev := <-s.events:
			ok = ev.Kind == KindDelivered && ev.OK && ev.SendID == "c1"
		case <-deadline:
			t.Fatal("typed command not confirmed")
		}
	}
	if p := s.Snapshot().Pending; len(p) != 0 {
		t.Fatalf("pending %v", p)
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
