package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePiStatus(t *testing.T, file string, st piStatus) {
	t.Helper()
	raw, _ := json.Marshal(st)
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, file); err != nil {
		t.Fatal(err)
	}
}

func nextState(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == KindState {
				return ev
			}
		case <-deadline:
			t.Fatal("no state event")
		}
	}
}

// A dialog pi's extension reports (ui_prompt_start) blocks the relay, with
// the dialog named, until it ends (ui_prompt_end); then pi is back to what
// it was doing. Nothing in between makes it look working or ready.
func TestPiDialogBlocksTheRelay(t *testing.T) {
	status := filepath.Join(t.TempDir(), "pi-status.json")
	h := &fakeHost{alive: true, screen: "0.4%/1.0M (auto)"}
	s := newSeat(Spec{StatusFile: status}, piAgent{label: "Pi"}, h, "x", "id")
	s.state = "working"
	a := piAgent{}
	writePiStatus(t, status, piStatus{State: "working", Prompt: "Replace the goal?"})
	s.reportDialog(a.reportedDialog(s))
	ev := nextState(t, s.events)
	if ev.State != "blocked" || !strings.Contains(ev.Detail, "Replace the goal?") || !strings.Contains(ev.Detail, "relay_control keys") {
		t.Fatalf("not blocked on the dialog: %+v", ev)
	}
	// Transcript news, a ready-looking screen and a send all leave it blocked.
	s.fold(obs{kind: "progress", text: "still here"})
	s.fold(obs{kind: "activity", text: "running x", tool: true})
	s.checkScreen(time.Now())
	if s.Snapshot().State != "blocked" {
		t.Fatalf("unblocked while the dialog is up: %s", s.Snapshot().State)
	}
	if _, err := s.Send("r1", "hello", "queue"); err == nil || len(h.typed) != 0 {
		t.Fatalf("typed into the dialog: %v %q", err, h.typed)
	}
	// The same dialog again says nothing new.
	s.reportDialog(a.reportedDialog(s))
	for _, ev := range drain(s.events) {
		if ev.Kind == KindState {
			t.Fatalf("state changed while blocked: %+v", ev)
		}
	}
	writePiStatus(t, status, piStatus{State: "working"})
	s.reportDialog(a.reportedDialog(s))
	if ev := nextState(t, s.events); ev.State != "working" {
		t.Fatalf("after the dialog: %+v", ev)
	}
	// A run that ends while a dialog is up is idle once the dialog ends.
	writePiStatus(t, status, piStatus{State: "working", Prompt: "confirm"})
	s.reportDialog(a.reportedDialog(s))
	nextState(t, s.events)
	s.fold(obs{kind: "turn", text: "done"})
	if s.Snapshot().State != "blocked" {
		t.Fatal("a turn end cleared the dialog")
	}
	writePiStatus(t, status, piStatus{State: "idle"})
	s.reportDialog(a.reportedDialog(s))
	if ev := nextState(t, s.events); ev.State != "idle" {
		t.Fatalf("after the dialog and the turn: %+v", ev)
	}
}

// The watcher reads the dialog on its own ticks, also before pi has a
// session file (a dialog at startup).
func TestPiDialogSeenByTheWatcher(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	status := filepath.Join(t.TempDir(), "pi-status.json")
	h := &fakeHost{alive: true}
	s := newSeat(Spec{StatusFile: status, Dir: t.TempDir()}, piAgent{label: "Pi"}, h, "x", "")
	s.state = "idle"
	go s.watch()
	defer s.Close()
	writePiStatus(t, status, piStatus{State: "idle", Prompt: "Pick a goal"})
	if ev := nextState(t, s.events); ev.State != "blocked" || !strings.Contains(ev.Detail, "Pick a goal") {
		t.Fatalf("watcher missed the dialog: %+v", ev)
	}
	writePiStatus(t, status, piStatus{State: "idle"})
	if ev := nextState(t, s.events); ev.State != "idle" {
		t.Fatalf("watcher missed the dialog ending: %+v", ev)
	}
}
