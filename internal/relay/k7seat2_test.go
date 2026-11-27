package relay

import (
	"errors"
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

// sayQuiet marked quiet as said after its KindQuiet went out. A Send begun
// while that event waited to be read (which clears the mark: not quiet
// while it is under way) and failed, saying nothing, had its clear
// overwritten: quiet again, the seat never said so.
func TestK7QuietIsSaidAgainAfterASendBegunAsItWasSaid(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	h := &k1Host{fakeHost: &fakeHost{alive: true}, paste: func() error { return errors.New("no such pane") }}
	s := newSeat(Spec{}, claudeAgent{}, h, "k7-said", "id")
	s.state, s.poll = "idle", piLook
	s.events = make(chan Event) // each event waits until it is read
	go s.watch()
	t.Cleanup(s.Close)
	sending := func() bool { // the watcher is in an emit, waiting for a reader
		if s.emitMu.TryLock() {
			s.emitMu.Unlock()
			return false
		}
		return true
	}
	if !eventually(sending) {
		t.Fatal("the watcher never said anything")
	}
	if _, err := s.Send("r1", "run the tests", "queue"); err == nil {
		t.Fatal("the paste failed; Send said it went")
	}
	if ev := <-s.events; ev.Kind != KindQuiet {
		t.Fatalf("first event %q; want the quiet of the first look", ev.Kind)
	}
	if got := quietOrder(t, s, 2*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("after the failed send: %q; want quiet said again", got)
	}
}

// The other half of SR5-4: once Claude has taken the queued message (its
// dequeue line, then the prompt with its words) and ended the turn it
// ran, the seat is quiet again.
func TestK7QuietOnceClaudeHasTakenItsQueuedMessage(t *testing.T) {
	s, _, a, _ := sr5ClaudeSeat(t, &fakeHost{alive: true, screen: claudePromptScreen}, "k7-queue", claudeUser(time.Now().Add(-time.Second), "sort the shelves"))
	go s.watch()
	t.Cleanup(s.Close)
	if !eventually(func() bool { return s.Snapshot().State == "working" }) {
		t.Fatal("never working")
	}
	s.track("r1", "also update the docs", "queue")
	reply := func(text string) string {
		return `{"type":"assistant","timestamp":"` + stamp(time.Now()) + `","message":{"content":[{"type":"text","text":"` + text + `"}]}}`
	}
	appendLines(t, a, k1Queue("enqueue", "also update the docs"), reply("Sorted."), k1TurnEnd())
	if !within(5*time.Second, func() bool { st := s.Snapshot(); return st.State == "idle" && len(st.Pending) == 0 }) {
		t.Fatal("never idle with r1 confirmed")
	}
	if s.Snapshot().Quiet {
		t.Fatal("quiet with r1 in Claude's queue")
	}
	appendLines(t, a, k1Queue("dequeue", ""), claudeUser(time.Now(), "also update the docs"), reply("Updated."), k1TurnEnd())
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("events %q; want quiet once Claude took r1 and ended its turn", got)
	}
}
