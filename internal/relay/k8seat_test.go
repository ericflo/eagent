package relay

// The sixteenth review's sixth pass over the seat (sr6seat).

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// sr6Queue is k1Queue at a given time.
func sr6Queue(op, content string, at time.Time) string {
	return fmt.Sprintf(`{"type":"queue-operation","operation":%q,"timestamp":"%s","content":%q}`, op, stamp(at), content)
}

// SR6-1. Claude's queue lives in its process. A Claude killed with a
// message in its queue (a restart: a model change applied while the agent
// was stuck, /restart, a crash) leaves an enqueue line with no dequeue in
// its transcript. The seat of the resumed Claude (Launch with resume: since
// is now, the last 4 MB are read as history) folds that line through the
// parser too: claudeParser.queued keeps it, holding() is true, and nothing
// later in the session takes it off (only a dequeue whose prompt is not a
// queued message's words, or an unknown queue line, clears the list). The
// seat is never quiet again: a model change asked later, with the agent
// idle between turns, waits for a KindQuiet that never comes, and the user
// was told it applies "once nothing waits on it".
func TestSr6SeatClaudeQueueLineLeftByARestartHoldsQuietForever(t *testing.T) {
	old := time.Now().Add(-2 * time.Minute)
	s, _, a, _ := sr6ClaudeSeat(t, "sr6-orphan",
		claudeUser(old, "sort the shelves"),
		sr6Queue("enqueue", "also update the docs", old.Add(time.Second)),
		// Claude was killed here; claude --resume appends to the same file.
	)
	s.since = time.Now() // Launch with resume
	go s.watch()
	t.Cleanup(s.Close)
	// The resumed Claude runs a whole turn of its own and ends it.
	if !within(3*time.Second, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.transcript != "" && s.looks > 3 }) {
		t.Fatal("never found the transcript")
	}
	appendLines(t, a, claudeTurnLines("hello again", "Hello.", time.Now())...)
	if !within(3*time.Second, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.state == "idle" && s.looks > 6 }) {
		t.Fatal("never idle after the turn")
	}
	got := quietOrder(t, s, looks(40))
	if got[len(got)-1] != "quiet" {
		s.mu.Lock()
		holds := s.queueHolds
		s.mu.Unlock()
		t.Fatalf("events %q, queueHolds %v: the resumed Claude is idle between turns with nothing in its queue, and the seat never says quiet (a waiting model change never applies)", got, holds)
	}
}

// sr6ClaudeSeat is sr5ClaudeSeat with a prompt screen.
func sr6ClaudeSeat(t *testing.T, name string, lines ...string) (s *Seat, spool, a, b string) {
	return sr5ClaudeSeat(t, &fakeHost{alive: true, screen: claudePromptScreen}, name, lines...)
}

// SR6-2. Claude's /compact runs for tens of seconds and writes nothing to
// the transcript until it is done (compact_boundary and the summary): the
// seat stays idle, and the hold on quiet that every command now gets ends
// quietMoveLooks looks (3.5 s) after the first idle look. The seat says
// quiet while Claude is still compacting, its spinner on the screen. A
// model change waiting for quiet ("Claude is acting on one of its own
// commands; it starts again with X once it has") then kills Claude in the
// middle of the compaction: the command recorded delivered never takes
// effect, and the user was told the change waits until it has.
func TestSr6SeatClaudeCompactIsQuietWhileItRuns(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	h := &fakeHost{alive: true, screen: claudePromptScreen}
	s, _, a, _ := sr5ClaudeSeat(t, h, "sr6-compact", claudeTurnLines("sort the shelves", "Sorted.", time.Now().Add(-time.Minute))...)
	go s.watch()
	t.Cleanup(s.Close)
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("never quiet at the start: %q", got)
	}
	if _, err := s.Send("c1", "/compact", "queue"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.screen = "✻ Compacting conversation… (esc to interrupt)\n> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	h.mu.Unlock()
	if !claudeSpinner.MatchString(h.screen) {
		t.Fatal("the test's screen is not Claude's busy line")
	}
	// Well past the command's own hold (quietMoveLooks looks after the
	// idle) and the two looks at rest: a compaction takes longer.
	got := quietOrder(t, s, looks(16))
	if slices.Contains(got, "quiet") {
		t.Fatalf("events %q: quiet while Claude is still compacting (its spinner up, no compact_boundary in %s)", got, a)
	}
}

// SR6-4. The other half of 9bc9bcc's window: holding() is
// len(queued) > taken, so a message Claude has dequeued counts as taken
// before its prompt line shows it running. Claude writes the dequeue, runs
// the UserPromptSubmit hooks, and only then records the prompt and starts
// the turn. A look that reads the turn end and the dequeue, and not yet the
// prompt, finds the seat idle, nothing pending (the enqueue line confirmed
// the send) and the queue holding nothing: quiet. A waiting model change
// then kills Claude as it starts the queued message's turn; the record
// says delivered, and it never runs.
func TestSr6SeatClaudeQuietBetweenADequeueAndItsPrompt(t *testing.T) {
	s, _, a, _ := sr6ClaudeSeat(t, "sr6-dequeue", claudeUser(time.Now().Add(-time.Second), "sort the shelves"))
	go s.watch()
	t.Cleanup(s.Close)
	if !eventually(func() bool { return s.Snapshot().State == "working" }) {
		t.Fatal("never working")
	}
	s.track("r1", "also update the docs", "queue") // typed while Claude works
	appendLines(t, a, k1Queue("enqueue", "also update the docs"))
	if got := quietOrder(t, s, looks(8)); !slices.Contains(got, "delivered r1") {
		t.Fatalf("events %q; want r1 confirmed by its enqueue line", got)
	}
	// The turn ends and Claude takes r1 off its queue; its prompt line
	// (after the prompt hooks) is not written yet.
	appendLines(t, a, `{"type":"assistant","timestamp":"`+stamp(time.Now())+`","message":{"content":[{"type":"text","text":"Sorted."}]}}`, k1TurnEnd(), k1Queue("dequeue", ""))
	got := quietOrder(t, s, looks(12))
	if got[len(got)-1] == "quiet" {
		t.Fatalf("events %q: quiet after Claude dequeued r1 and before its prompt shows it running; want not quiet until the prompt line", got)
	}
}

// sr6Codex is Codex with its queue command stubbed: the hand-over goes.
type sr6Codex struct{ codexAgent }

func (sr6Codex) afterTurn(*Seat, string, string) error { return nil }

// SR6-3. The sibling of 9bc9bcc and 04b4c03 on the after-turn path: one
// of Codex's own commands sent "after this turn" while Codex works is
// handed to Codex's own queue (codex queue), and Send says it delivered at
// once (the agent runs it rather than recording a message). It sets no
// hold on quiet: no moveLook, no pending, no held. The look that reads the
// turn's end finds the seat idle with nothing waiting and says quiet, while
// Codex has not yet taken the command off its queue. A model change
// waiting for quiet then kills Codex with the command still queued: the
// record says delivered, and it never runs. The same command typed (queue
// mode) holds quiet until quietMoveLooks looks after the idle. Antigravity's
// hand-over (typed into its own queue) takes the same branch.
func TestSr6SeatCommandInCodexsOwnQueueIsQuietAtTheTurnEnd(t *testing.T) {
	path := t.TempDir() + "/rollout.jsonl"
	writeLines(t, path, k1CodexMeta(), fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"task_started"}}`, stamp(time.Now())))
	s := newSeat(Spec{}, sr6Codex{}, &fakeHost{alive: true}, "sr6-codex-queue", "thread-1")
	s.transcript, s.state, s.poll = path, "working", piLook
	go s.watch()
	t.Cleanup(s.Close)
	if !within(3*time.Second, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.looks > 2 }) {
		t.Fatal("never looked")
	}
	res, err := s.Send("c1", "/compact", "after_turn")
	if err != nil {
		t.Fatal(err)
	}
	appendLines(t, path, fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Done."}}`, stamp(time.Now())))
	if !within(3*time.Second, func() bool { return s.Snapshot().State == "idle" }) {
		t.Fatal("never idle after the turn")
	}
	// Codex takes its queue after the turn; within a couple of looks of the
	// turn end it has not.
	quietSoon := within(looks(2), func() bool { return s.Snapshot().Quiet })
	if quietSoon {
		t.Fatalf("Send said %q; the seat is quiet within two looks of the turn end, with /compact still in Codex's own queue; want it held as a typed command is (until quietMoveLooks looks after the idle)", res)
	}
}
