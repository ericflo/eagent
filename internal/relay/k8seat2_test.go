package relay

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Quiet needs the agent's own word that it is at rest (atRest): pi's
// status file; Claude's, Codex's and Antigravity's screen at their prompt
// with no busy line. The screens are the ones the seat already tells apart
// (TestAQuietAgentStaysBlockedWhileItsScreenSaysItIsBusy).
func TestK8AnAgentIsAtRestOnlyByItsOwnWord(t *testing.T) {
	for _, c := range []struct {
		agent      Agent
		busy, rest string
	}{
		{claudeAgent{}, "✻ Thinking… (10m 12s · esc to interrupt)\n  ⏵⏵ bypass permissions on (shift+tab to cycle)", "> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		{codexAgent{}, "• Working (10m 3s • esc to interrupt)\n› Ask Codex to do anything", "› Ask Codex to do anything\n  ? for shortcuts"},
		{antigravityAgent{}, "thinking\nAntigravity relay · running · Gemini 3.8", "> \nAntigravity relay · idle · Gemini 3.8"},
	} {
		h := &fakeHost{alive: true}
		s := newSeat(Spec{}, c.agent, h, "k8-rest-"+c.agent.Name(), "")
		for screen, want := range map[string]bool{c.busy: false, c.rest: true, "": false, "Do you want to proceed?\n❯ 1. Yes": false} {
			h.mu.Lock()
			h.screen = screen
			h.mu.Unlock()
			if got := s.atRest(); got != want {
				t.Errorf("%s at rest %v on %q; want %v", c.agent.Name(), got, screen, want)
			}
		}
	}
	isolatePi(t)
	status := filepath.Join(t.TempDir(), "pi-status.json")
	s := newSeat(Spec{StatusFile: status}, piAgent{name: "pi", label: "Pi", binary: "pi"}, &fakeHost{alive: true, screen: piFooterScreen}, "k8-rest-pi", "")
	for _, c := range []struct {
		st   piStatus
		want bool
	}{
		{piStatus{State: "idle"}, true},
		{piStatus{State: "working"}, false},
		{piStatus{State: "idle", Prompt: "Allow the edit?"}, false},
	} {
		writePiStatus(t, status, c.st)
		if got := s.atRest(); got != c.want {
			t.Errorf("pi at rest %v with status %+v; want %v (its footer is always drawn)", got, c.st, c.want)
		}
	}
}

// SR6-1, for its own reason: the queue a killed Claude left in the
// history holds nothing (not quiet only after quietQueueLooks).
func TestK8AQueueLineReadAsHistoryHoldsNothing(t *testing.T) {
	old := time.Now().Add(-2 * time.Minute)
	s, _, a, _ := sr6ClaudeSeat(t, "k8-history", claudeUser(old, "sort the shelves"), sr6Queue("enqueue", "also update the docs", old.Add(time.Second)))
	s.since = time.Now()
	go s.watch()
	t.Cleanup(s.Close)
	if !within(3*time.Second, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.transcript != "" && s.looks > 2 }) {
		t.Fatal("never read the transcript")
	}
	if s.mu.Lock(); s.queueHolds {
		s.mu.Unlock()
		t.Fatal("a queue line read as history holds quiet")
	}
	s.mu.Unlock()
	appendLines(t, a, claudeTurnLines("hello again", "Hello.", time.Now())...)
	if got := quietOrder(t, s, looks(quietQueueLooks/2)); got[len(got)-1] != "quiet" {
		t.Fatalf("events %q; want quiet once Claude has rested two looks after its turn", got)
	}
}

// No one input holds quiet for ever: a message the transcript shows in
// Claude's queue that no line ever takes off it gives way once Claude has
// been at rest quietQueueLooks looks in a row.
func TestK8AQueueHoldGivesWayToALongRest(t *testing.T) {
	saved := quietQueueLooks
	quietQueueLooks = 6
	t.Cleanup(func() { quietQueueLooks = saved })
	s, _, a, _ := sr6ClaudeSeat(t, "k8-stale", claudeUser(time.Now().Add(-time.Second), "sort the shelves"))
	go s.watch()
	t.Cleanup(s.Close)
	if !eventually(func() bool { return s.Snapshot().State == "working" }) {
		t.Fatal("never working")
	}
	s.track("r1", "also update the docs", "queue")
	appendLines(t, a, k1Queue("enqueue", "also update the docs"), `{"type":"assistant","timestamp":"`+stamp(time.Now())+`","message":{"content":[{"type":"text","text":"Sorted."}]}}`, k1TurnEnd())
	if !within(3*time.Second, func() bool { st := s.Snapshot(); return st.State == "idle" && len(st.Pending) == 0 }) {
		t.Fatal("never idle with r1 confirmed")
	}
	if within(looks(3), func() bool { return s.Snapshot().Quiet }) {
		t.Fatal("quiet right after the turn, with r1 still in Claude's queue")
	}
	if got := quietOrder(t, s, looks(4*quietQueueLooks)); got[len(got)-1] != "quiet" {
		t.Fatalf("events %q; want the queue's hold to give way to a long rest", got)
	}
}

// SR6-3, for its own reason: with Codex at its composer (at rest on its
// screen), a command handed to Codex's own queue holds quiet as a typed
// command does, and gives way quietMoveLooks looks after the idle.
func TestK8ACommandInCodexsOwnQueueHoldsAsATypedOne(t *testing.T) {
	path := t.TempDir() + "/rollout.jsonl"
	writeLines(t, path, k1CodexMeta(), fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"task_started"}}`, stamp(time.Now())))
	s := newSeat(Spec{}, sr6Codex{}, &fakeHost{alive: true, screen: "› Ask Codex to do anything\n  ? for shortcuts"}, "k8-codex-queue", "thread-1")
	s.transcript, s.state, s.poll = path, "working", piLook
	go s.watch()
	t.Cleanup(s.Close)
	if !within(3*time.Second, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.looks > 2 }) {
		t.Fatal("never looked")
	}
	if _, err := s.Send("c1", "/compact", "after_turn"); err != nil {
		t.Fatal(err)
	}
	appendLines(t, path, fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Done."}}`, stamp(time.Now())))
	if !within(3*time.Second, func() bool { return s.Snapshot().State == "idle" }) {
		t.Fatal("never idle after the turn")
	}
	if within(looks(3), func() bool { return s.Snapshot().Quiet }) {
		t.Fatal("quiet within three looks of the turn end, /compact still in Codex's own queue")
	}
	if got := quietOrder(t, s, looks(4*quietMoveLooks)); got[len(got)-1] != "quiet" {
		t.Fatalf("events %q; want quiet once the command's hold is over", got)
	}
}
