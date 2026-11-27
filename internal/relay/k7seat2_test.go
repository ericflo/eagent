package relay

import (
	"testing"
	"time"
)

// A command typed while the agent works (Claude queues it) runs after the
// turn: the wait for the move it may make is counted from the first look
// that finds the agent idle again, not from the typing. Counted from the
// typing, a turn longer than quietMoveLooks used the wait up, and the seat
// said quiet at the turn's end, before Claude's SessionStart hook named the
// session the queued /clear started.
func TestK7ACommandQueuedMidTurnIsWaitedOnFromTheIdleAfterIt(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	s, spool, a, b := sr5ClaudeSeat(t, &fakeHost{alive: true, screen: claudePromptScreen}, "k7-midturn", claudeUser(time.Now().Add(-time.Second), "sort the shelves"))
	go s.watch()
	t.Cleanup(s.Close)
	if !eventually(func() bool { return s.Snapshot().State == "working" }) {
		t.Fatal("never working")
	}
	if _, err := s.Send("c1", "/clear", "queue"); err != nil {
		t.Fatal(err)
	}
	start := func() int { s.mu.Lock(); defer s.mu.Unlock(); return s.looks }()
	if !within(5*time.Second, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.looks > start+quietMoveLooks+2 }) {
		t.Fatal("the seat stopped looking")
	}
	appendLines(t, a, `{"type":"assistant","timestamp":"`+stamp(time.Now())+`","message":{"content":[{"type":"text","text":"Sorted."}]}}`, k1TurnEnd())
	if !within(5*time.Second, func() bool { return s.Snapshot().State == "idle" }) {
		t.Fatal("never idle after the turn")
	}
	sr4ClaudeHook(t, spool, "b-id", b, "clear")
	got := quietOrder(t, s, 5*time.Second)
	if n := len(got); n < 2 || got[n-1] != "quiet" || got[n-2] != "inner b-id" {
		t.Fatalf("events %q; want the move to b-id the queued /clear made said before quiet", got)
	}
}
