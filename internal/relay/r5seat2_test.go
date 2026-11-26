package relay

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// A reattached Codex or pi seat reads its transcript from the start (it is
// under tailBackfill), and the first line is the session header, a "meta"
// observation. The history after it still says the agent is mid-turn.
func TestAReattachedCodexOrPiSeatMidTurnIsWorking(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 24, 10, 0, 30, 0, time.UTC)
	ts := `"timestamp":"2026-11-24T10:00:0`
	for _, c := range []struct {
		name  string
		agent Agent
		lines []string
	}{
		{"codex", codexAgent{}, []string{
			`{` + ts + `0Z","type":"session_meta","payload":{"id":"01a1814b","cwd":"/w","timestamp":"2026-11-24T10:00:00Z"}}`,
			`{` + ts + `1Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`,
			`{` + ts + `2Z","type":"event_msg","payload":{"type":"user_message","message":"build it"}}`,
		}},
		{"pi", piAgent{label: "Pi"}, []string{piHeader, piModelChange, piUser, piToolUse, piToolResult}},
		// Control: the same pi history without its header line (as when a
		// long transcript is read from its last 4 MB) says working.
		{"pi-control-no-header", piAgent{label: "Pi"}, []string{piModelChange, piUser, piToolUse, piToolResult}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSeat(Spec{}, c.agent, &fakeHost{alive: true}, "x", "")
			s.markAttached(since)
			p := c.agent.newParser(Spec{})
			for _, l := range c.lines {
				for _, o := range p.line([]byte(l)) {
					s.fold(o)
				}
			}
			if st := s.Snapshot().State; st != "working" {
				t.Fatalf("reattached mid-turn %s seat is %s, want working", c.name, st)
			}
		})
	}
}

// A working agent that went quiet for ten minutes because a dialog the seat
// does not know is up (no prompt, no busy line on screen) is blocked, and
// a send to it is refused: its text and Enter would answer the dialog.
func TestAQuietSeatDoesNotTypeIntoADialogItDoesNotKnow(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true, screen: " Which approach should I take?\n\n ❯ 1. Rewrite the parser\n   2. Patch the caller\n   3. Type something.\n\n Enter to select · ↑/↓ to navigate · Esc to cancel"}
	s := newSeat(Spec{Name: "claude", Profile: config.RelayProfile{Agent: "claude"}}, claudeAgent{}, h, "x", "")
	start := time.Now()
	s.state, s.stateAt, s.lastLine = "working", start.Add(-11*time.Minute), start.Add(-11*time.Minute)
	s.checkScreen(start)
	if st := s.Snapshot().State; st != "blocked" {
		t.Fatalf("seat is %s", st)
	}
	s.checkScreen(start.Add(1500 * time.Millisecond))
	if _, err := s.Send("r1", "status?", "queue"); err == nil {
		t.Fatalf("a send was typed into a dialog: typed %q, keys %q", h.typed, h.keys)
	}
}

// Two long sends that share their first 160 bytes: the transcript shows
// only the first, and only the first is confirmed. The second (its Enter
// lost, say) stays watched.
func TestLongSendsWithACommonStartAreNotBothConfirmed(t *testing.T) {
	t.Parallel()
	intro := strings.Repeat("CI failed on main. Read the log below, find the cause and fix it. ", 3)
	a := intro + "\nFAIL internal/relay TestOne"
	b := intro + "\nFAIL internal/harness TestTwo"
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.track("r1", a, "queue")
	s.track("r2", b, "queue")
	if ids := s.matchPending(a); len(ids) != 1 || ids[0] != "r1" {
		t.Fatalf("the arrival of r1 confirmed %v", ids)
	}
}

// A long send the transcript shows in full, or shortened, or reflowed, or
// joined with another held message (Claude's Stop hook), is still
// confirmed.
func TestALongSendIsConfirmedHoweverTheTranscriptShowsIt(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("Read the log and fix the failing test. ", 8) + "The end."
	for name, got := range map[string]string{
		"whole":     long,
		"shortened": long[:200] + "…",
		"reflowed":  strings.ReplaceAll(long, ". ", ".\n  "),
		"joined":    long + "\n\nand then a second held message",
		"after":     "first held message\n\n" + long,
	} {
		s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
		s.track("r1", long, "queue")
		if ids := s.matchPending(got); len(ids) != 1 {
			t.Fatalf("%s: confirmed %v", name, ids)
		}
	}
}

// A quiet seat whose screen shows the agent's own prompt or busy line
// still takes a message, typed as into a working agent.
func TestAQuietSeatAtItsPromptStillTakesAMessage(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true, screen: "> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
	s := newSeat(Spec{Name: "claude"}, claudeAgent{}, h, "x", "")
	s.state, s.hung = "blocked", true
	if msg, err := s.Send("r1", "status?", "queue"); err != nil || len(h.typed) != 1 {
		t.Fatalf("send: %q %v, typed %q", msg, err, h.typed)
	}
}

// The seat types one message at a time: a held message it releases and a
// send that arrives meanwhile never mix their pastes and Enters.
func TestTwoMessagesTypedAtOnceDoNotMix(t *testing.T) {
	saved := typeSettle
	typeSettle = 30 * time.Millisecond
	t.Cleanup(func() { typeSettle = saved })
	h := &orderHost{fakeHost: fakeHost{alive: true}}
	s := newSeat(Spec{}, claudeAgent{}, h, "x", "")
	var wg sync.WaitGroup
	for _, text := range []string{"one", "two"} {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.typeText(text) }()
	}
	wg.Wait()
	got := strings.Join(h.order, " ")
	if got != "paste:one Enter paste:two Enter" && got != "paste:two Enter paste:one Enter" {
		t.Fatalf("typed %s", got)
	}
}

// orderHost records pastes and keys in the order they came.
type orderHost struct {
	fakeHost
	order []string
}

func (h *orderHost) Paste(target, text string) error {
	h.mu.Lock()
	h.order = append(h.order, "paste:"+text)
	h.mu.Unlock()
	return nil
}

func (h *orderHost) Keys(target string, keys ...string) error {
	h.mu.Lock()
	h.order = append(h.order, keys...)
	h.mu.Unlock()
	return nil
}

// A held "/" command the seat releases runs as Claude's command, which
// writes no message: it is confirmed when typed, not reported lost.
func TestAReleasedHeldCommandIsNotReportedLost(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: "> /compact\n  ⏵⏵ bypass permissions on"}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("c1", "/compact", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.fold(obs{kind: "aborted"})
	s.releaseHeld(time.Now().Add(time.Second))
	if len(h.typed) != 1 || h.typed[0] != "/compact" {
		t.Fatalf("typed %q", h.typed)
	}
	start := time.Now()
	for _, after := range []time.Duration{7, 14, 60} {
		s.checkScreen(start.Add(after * time.Second))
	}
	var evs []Event
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered {
			evs = append(evs, ev)
		}
	}
	if len(evs) != 1 || !evs[0].OK || slices.Contains(h.keys[1:], "Enter") {
		t.Fatalf("deliveries %+v, keys %q", evs, h.keys)
	}
}

// An after-turn message typed in because the agent has no hand-over of its
// own, and left in the input box when the turn was interrupted, gets no
// Enter until the agent has stayed idle a while: whatever the interrupt
// was for goes first, as for pi.
func TestAnAfterTurnMessageIsNotPressedRightAfterAnInterrupt(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true, screen: "> then run the tests\n  ⏵⏵ bypass permissions on"}
	s := newSeat(Spec{Name: "claude"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("r1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.keys = nil
	h.mu.Unlock()
	start := time.Now().Add(10 * time.Second)
	s.mu.Lock()
	s.state, s.stateAt = "idle", start.Add(-time.Second) // interrupted a second ago
	s.mu.Unlock()
	s.checkScreen(start)
	if len(h.keys) != 0 {
		t.Fatalf("pressed %q a second after the interrupt", h.keys)
	}
	s.checkScreen(start.Add(heldGrace))
	if strings.Join(h.keys, " ") != "Enter" {
		t.Fatalf("keys %q once the agent stayed idle", h.keys)
	}
}

// A Claude relay whose transcript does not exist (restarted before its
// first message) still types a held message left from before the restart.
func TestClaudeWithoutATranscriptStillReleasesAHeldMessage(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	spool := filepath.Join(t.TempDir(), "after_turn")
	if err := spoolMessage(spool, "r1", "kept over the restart"); err != nil {
		t.Fatal(err)
	}
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "0b8f6a52-6d1e-4c8e-9f55-3f2d8b8f1a11")
	s.state, s.poll = "idle", 5*time.Millisecond
	go s.watch()
	defer s.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.mu.Lock()
		n := len(h.typed)
		h.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the held message was never typed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(spool); err != nil {
		t.Fatal(err)
	}
}
