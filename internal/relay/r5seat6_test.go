package relay

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Claude's idle screen as it draws it in bypass mode: the last answer, a
// rule, the empty prompt, a rule, the footer.
func claudeIdleAfter(answer string) string {
	return answer + "\n\n" +
		"────────────────────────────────────────────────────────────\n" +
		"> \n" +
		"────────────────────────────────────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle)"
}

func reportsOf(s *Seat, h *fakeHost) (typed []string, lost []string) {
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && !ev.OK {
			lost = append(lost, ev.SendID)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.typed...), lost
}

// The spinner line is read by its shape: a bullet or a sentence in the
// last answer that quotes it is not Claude working, and the spinner drawn
// above a todo list is.
func TestTheSpinnerLineIsReadByItsShape(t *testing.T) {
	t.Parallel()
	quoted := claudeIdleAfter("⏺ Done. What changed:\n" +
		"  - busy() reads the spinner line by its shape\n" +
		"  - a line like \"✻ Thinking… (esc to interrupt)\" counts there\n" +
		"  says \"esc to interrupt\", so the seat leaves a quiet agent blocked.")
	todo := "⏺ Bash(go test ./...)\n  ⎿  Running…\n\n" +
		"✻ Running the test suite… (14m 2s · ↓ 3.1k tokens · esc to interrupt)\n" +
		"  ⎿  ☒ Fix the parser\n     ☐ Run the test suite\n     ☐ Commit\n\n" +
		"────────────────────────────────────────────────────────────\n> \n" +
		"────────────────────────────────────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	codex := "• Working (10m 3s • esc to interrupt)\n› Ask Codex to do anything"
	for _, c := range []struct {
		agent  busyScreen
		screen string
		want   bool
	}{
		{claudeAgent{}, quoted, false},
		{claudeAgent{}, todo, true},
		{claudeAgent{}, "✻ Thinking… (10m 12s · esc to interrupt)\n  ⏵⏵ bypass permissions on", true},
		{claudeAgent{}, "✽ Compacting conversation… (esc to interrupt)\n  ⏵⏵ bypass permissions on", true},
		// Each agent's own glyphs only: Codex's bullet is not Claude's spinner.
		{claudeAgent{}, codex, false},
		{codexAgent{}, codex, true},
		{codexAgent{}, "  - quoted: ✻ Thinking… (esc to interrupt)\n› Ask Codex to do anything", false},
	} {
		if got := c.agent.busy(c.screen); got != c.want {
			t.Errorf("%T busy = %v for\n%s", c.agent, got, c.screen)
		}
	}
}

// Claude idle at its prompt after an interrupt, its last answer quoting the
// busy line: a left-over held message is reported, not stranded and not
// typed.
func TestALeftOverIsReportedWhenTheAnswerQuotesTheBusyLine(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: claudeIdleAfter("⏺ Done.\n  - a line like \"✻ Thinking… (esc to interrupt)\" counts")}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	if err := spoolMessage(spool, "m1", "then run the tests"); err != nil {
		t.Fatal(err)
	}
	s.state = "working"
	s.fold(obs{kind: "aborted"})
	s.reportLeftOvers(time.Now().Add(time.Second))
	if typed, lost := reportsOf(s, h); len(typed) != 0 || strings.Join(lost, ",") != "m1" {
		t.Fatalf("typed %q, lost %v", typed, lost)
	}
}

// Claude mid-turn with its todo list under the spinner, quiet ten minutes:
// the seat stays blocked (the spinner shows), and the message held for the
// end of the turn is neither typed nor reported; the Stop hook has it.
func TestAHeldMessageWaitsThroughAQuietTurnShowingItsTodoList(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	screen := "✻ Running the test suite… (14m 2s · esc to interrupt)\n" +
		"  ⎿  ☒ Fix the parser\n     ☐ Run the test suite\n     ☐ Commit\n\n" +
		"────────────────────────────────────────────────────────────\n> \n" +
		"────────────────────────────────────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	h := &fakeHost{alive: true, screen: screen}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("m1", "when you are done, commit and push", "after_turn"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.mu.Lock()
	s.stateAt, s.lastLine = now.Add(-11*time.Minute), now.Add(-11*time.Minute)
	s.mu.Unlock()
	s.checkScreen(now)
	s.checkScreen(now.Add(2 * time.Second))
	s.reportLeftOvers(now.Add(time.Hour))
	typed, lost := reportsOf(s, h)
	if st := s.Snapshot().State; st != "blocked" || len(typed) != 0 || len(lost) != 0 {
		t.Fatalf("state %s, typed %q, lost %v", st, typed, lost)
	}
	if names, _ := filepath.Glob(filepath.Join(spool, "*.txt")); len(names) != 1 {
		t.Fatalf("spool %v", names)
	}
}

// A person typing a correction into Claude's box after an interrupt: the
// seat pastes nothing onto it; the left-over is reported.
func TestNothingIsPastedOntoTextInTheInputBox(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: "⏺ I'll rewrite the parser first.\n  ⎿  Interrupted by user\n\n> no, keep the parser and patch the caller inst\n  ⏵⏵ bypass permissions on"}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	if err := spoolMessage(spool, "m1", "then run the tests"); err != nil {
		t.Fatal(err)
	}
	s.state = "working"
	s.fold(obs{kind: "aborted"})
	s.reportLeftOvers(time.Now().Add(time.Second))
	if typed, lost := reportsOf(s, h); len(typed) != 0 || len(h.keys) != 0 || strings.Join(lost, ",") != "m1" {
		t.Fatalf("typed %q, keys %q, lost %v", typed, h.keys, lost)
	}
}

// An after-turn "/compact" handed to agy's own queue never shows in the
// transcript; it is not reported lost, as at 80f4746.
func TestAnAfterTurnCommandInAgysQueueIsNotReportedLost(t *testing.T) {
	saved := typeSettle
	typeSettle = time.Millisecond
	t.Cleanup(func() { typeSettle = saved })
	h := &fakeHost{alive: true, screen: "> \n\nAntigravity relay · idle · Gemini 3 Pro"}
	s := newSeat(Spec{Name: "antigravity"}, antigravityAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("c1", "/compact", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.fold(obs{kind: "turn", at: time.Now()})
	start := time.Now()
	for _, after := range []time.Duration{7, 20, 50, 70} {
		s.checkScreen(start.Add(after * time.Second))
	}
	if _, lost := reportsOf(s, h); len(lost) != 0 {
		t.Fatalf("reported lost: %v", lost)
	}
}

// An after-turn message in agy's queue while agy runs a long quiet tool
// call: blocked for being quiet, and the message is not reported lost.
func TestAnAfterTurnMessageIsNotReportedLostWhileTheTurnRuns(t *testing.T) {
	saved := typeSettle
	typeSettle = time.Millisecond
	t.Cleanup(func() { typeSettle = saved })
	h := &fakeHost{alive: true, screen: "> \n\nAntigravity relay · running · Gemini 3 Pro"}
	s := newSeat(Spec{Name: "antigravity"}, antigravityAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("m1", "when you are done, commit and push", "after_turn"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.mu.Lock()
	s.stateAt, s.lastLine = now.Add(-11*time.Minute), now.Add(-11*time.Minute)
	for _, p := range s.pending {
		p.at = now.Add(-11 * time.Minute)
	}
	s.mu.Unlock()
	s.checkScreen(now)
	for _, after := range []time.Duration{20, 40, 60} {
		s.checkScreen(time.Now().Add(after * time.Second))
	}
	if _, lost := reportsOf(s, h); len(lost) != 0 {
		t.Fatalf("reported lost: %v", lost)
	}
}

// A seat reattached to a Claude whose transcript ends with its turn over,
// with a message left in the spool, reports it as a live seat would.
func TestAReattachedIdleSeatReportsALeftOver(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	if err := spoolMessage(spool, "m1", "then run the tests"); err != nil {
		t.Fatal(err)
	}
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	since := time.Now()
	s.markAttached(since)
	for i, o := range []obs{{kind: "working"}, {kind: "turn", text: "Done."}} {
		o.at = since.Add(time.Duration(i-2) * time.Second)
		s.fold(o)
	}
	s.reportLeftOvers(time.Now().Add(time.Second))
	if _, lost := reportsOf(s, h); strings.Join(lost, ",") != "m1" {
		t.Fatalf("lost %v", lost)
	}
}

// Claude records a message queued during a turn as an "enqueue" user line
// with no working of its own. In re-read history after a turn end, it does
// not make the reattached seat say working, as it does not for the live
// watcher.
func TestAReattachedSeatDoesNotTakeAnEnqueueLineForWork(t *testing.T) {
	t.Parallel()
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	since := time.Now()
	s.markAttached(since)
	for i, o := range []obs{{kind: "working"}, {kind: "turn", text: "Done."}, {kind: "user", text: "queued later"}} {
		o.at = since.Add(time.Duration(i-3) * time.Second)
		s.fold(o)
	}
	if st := s.Snapshot().State; st != "idle" {
		t.Fatalf("seat is %s", st)
	}
}
