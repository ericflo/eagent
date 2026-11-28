package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Claude idle at its prompt, its last answer a markdown list whose last
// item ends with a quoted working line (common while Claude works on eagent
// itself). A bullet starts with "-", which is none of Claude's spinner
// glyphs, so it is not busy: a quiet seat at that prompt goes idle, and an
// after-turn message to it is typed at once.
func TestABulletEndingInAQuotedWorkingLineIsNotBusy(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	screen := claudeIdleAfter("⏺ Done. The busy check now matches both agents:\n" +
		"  - Claude: ✻ Thinking… (12s · esc to interrupt)\n" +
		"  - Codex: • Working (3s • esc to interrupt)")
	if (claudeAgent{}).busy(screen) {
		t.Errorf("busy = true for Claude's idle prompt:\n%s", screen)
	}
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: screen}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	now := time.Now()
	s.mu.Lock()
	s.state, s.hung, s.stateAt, s.lastLine = "blocked", true, now.Add(-11*time.Minute), now.Add(-11*time.Minute)
	s.mu.Unlock()
	s.checkScreen(now)
	s.checkScreen(now) // two checks in a row agree
	if st := s.Snapshot().State; st != "idle" {
		t.Errorf("a quiet Claude at its idle prompt stays %s", st)
	}
	if _, err := s.Send("m1", "then commit", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.reportLeftOvers(now.Add(time.Hour))
	typed, lost := reportsOf(s, h)
	names, _ := filepath.Glob(filepath.Join(spool, "*.txt"))
	if len(typed) == 0 && len(lost) == 0 {
		t.Errorf("the after-turn message was neither typed nor reported; spool holds %v", names)
	}
}

// The Stop hook and the seat's report take from one spool at once: every
// message is either handed over or reported, never both and never neither.
func TestTheHookAndTheSeatNeverBothTakeOneMessage(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	for round := 0; round < 20; round++ {
		spool := filepath.Join(t.TempDir(), "after_turn")
		const n = 40
		for i := 0; i < n; i++ {
			if err := spoolMessage(spool, fmt.Sprintf("m%d", i), fmt.Sprintf("message number %d", i)); err != nil {
				t.Fatal(err)
			}
		}
		s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
		s.state, s.turnEndAt = "idle", time.Now().Add(-time.Minute)
		var out bytes.Buffer
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); RunStopHook(spool, strings.NewReader("{}"), &out) }()
		go func() { defer wg.Done(); s.reportLeftOvers(time.Now()) }()
		wg.Wait()
		handed := map[string]bool{}
		if out.Len() > 0 {
			var d struct{ Reason string }
			if err := json.Unmarshal(out.Bytes(), &d); err != nil {
				t.Fatal(err)
			}
			for _, part := range strings.Split(d.Reason, "\n\n") {
				handed["m"+strings.TrimPrefix(part, "message number ")] = true
			}
		}
		_, lost := reportsOf(s, &fakeHost{})
		for _, id := range lost {
			if handed[id] {
				t.Fatalf("round %d: %s handed over and reported", round, id)
			}
			handed[id] = true
		}
		if len(handed) != n {
			t.Fatalf("round %d: %d of %d accounted for", round, len(handed), n)
		}
		if left, _ := filepath.Glob(filepath.Join(spool, "*")); len(left) != 0 {
			t.Fatalf("round %d: left %v", round, left)
		}
	}
}

// Nothing after-turn is pressed on a timer: a held message in Claude's
// spool, and one typed into an idle Codex, all old,
// their text on screen, the agent idle. The held ones are not reported
// either; the one typed into the idle Codex, which nothing holds, is
// reported unconfirmed when it never arrives (finding 6 of round 7).
func TestAfterTurnSendsAreNeverNudgedOrReported(t *testing.T) {
	saved, savedSettle := heldGrace, typeSettle
	heldGrace, typeSettle = time.Hour, time.Millisecond
	t.Cleanup(func() { heldGrace, typeSettle = saved, savedSettle })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: claudeIdleAfter("> then commit")}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if _, err := s.Send("m1", "then commit", "after_turn"); err != nil {
		t.Fatal(err)
	}
	hc := &fakeHost{alive: true, screen: "› then push\n\n› Ask Codex to do anything"}
	c := newSeat(Spec{Name: "codex"}, codexAgent{}, hc, "y", "")
	c.state = "idle"
	if _, err := c.Send("m2", "then push", "after_turn"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for _, seat := range []*Seat{s, c} {
		seat.mu.Lock()
		seat.state, seat.stateAt = "idle", start.Add(-time.Hour)
		for _, p := range seat.pending {
			p.at = start.Add(-time.Hour)
		}
		seat.mu.Unlock()
		for _, after := range []time.Duration{0, 7, 20, 50, 70, 200} {
			seat.checkScreen(start.Add(after * time.Second))
		}
	}
	_, lost := reportsOf(s, h)
	_, lostC := reportsOf(c, hc)
	h.mu.Lock()
	keys := append([]string(nil), h.keys...)
	h.mu.Unlock()
	hc.mu.Lock()
	keysC := append([]string(nil), hc.keys...)
	hc.mu.Unlock()
	if len(lost) != 0 || !slices.Equal(lostC, []string{"m2"}) || len(keys) != 0 || len(keysC) != 1 {
		t.Fatalf("lost %v %v, claude keys %q, codex keys %q (one Enter: the typing)", lost, lostC, keys, keysC)
	}
}
