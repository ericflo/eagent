package relay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// deliveries are the KindDelivered events among evs.
func deliveries(evs []Event) []Event {
	var out []Event
	for _, ev := range evs {
		if ev.Kind == KindDelivered {
			out = append(out, ev)
		}
	}
	return out
}

// heldClaudeSeat is a working Claude seat with its Stop hook, and a message
// held for the end of its turn.
func heldClaudeSeat(t *testing.T, text string) (*Seat, *fakeHost, string) {
	t.Helper()
	spool := filepath.Join(t.TempDir(), "after_turn")
	// The held words on screen: a press at them would be an Enter eagent
	// must not send for a message it never typed.
	h := &fakeHost{alive: true, screen: "  " + text + "\n" + claudePromptScreen}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if msg, err := s.Send("r1", text, "after_turn"); err != nil || !strings.Contains(msg, "held for") {
		t.Fatalf("send: %q %v", msg, err)
	}
	return s, h, spool
}

// killedHook is the Stop hook killed after it claimed and deleted the spool
// file, before it printed: Claude never gets the text.
func killedHook(spool string) {
	taken, _ := takeSpool(spool)
	for _, path := range taken {
		_ = os.Remove(path)
	}
}

// A held message whose Stop hook took its file but never handed it over is
// reported unconfirmed once, the usual wait after the seat saw its turn
// over and its file gone; never while the turn runs, never typed or pressed.
func TestBSeatHeldMessageTheHookLostIsReportedUnconfirmed(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	s, h, spool := heldClaudeSeat(t, "then run the tests")
	killedHook(spool)
	// The turn still runs: hours later nothing is reported.
	late := time.Now().Add(3 * time.Hour)
	s.reportLeftOvers(late)
	s.checkScreen(late)
	if got := deliveries(drain(s.events)); len(got) != 0 {
		t.Fatalf("reported while its turn ran: %+v", got)
	}
	s.fold(obs{kind: "turn", text: "Done."})
	drain(s.events)
	seen := time.Now().Add(20 * time.Second)
	s.reportLeftOvers(seen) // the turn is over and the file is gone
	s.checkScreen(seen.Add(44 * time.Second))
	if got := deliveries(drain(s.events)); len(got) != 0 {
		t.Fatalf("reported before the wait was over: %+v", got)
	}
	for _, after := range []time.Duration{46, 60, 120} {
		s.reportLeftOvers(seen.Add(after * time.Second))
		s.checkScreen(seen.Add(after * time.Second))
	}
	got := deliveries(drain(s.events))
	if len(got) != 1 || got[0].SendID != "r1" || got[0].OK || got[0].Outcome != OutcomeUnconfirmed {
		t.Fatalf("deliveries %+v, want r1 unconfirmed once", got)
	}
	if len(h.typed) != 0 || len(h.keys) != 0 {
		t.Fatalf("typed %q, pressed %q", h.typed, h.keys)
	}
}

// A held message the hook handed over is confirmed by its arrival, and never
// reported unconfirmed, whether it arrives before or after the seat sees
// its file gone.
func TestBSeatHeldMessageThatArrivesIsConfirmed(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	for _, early := range []bool{true, false} {
		s, h, spool := heldClaudeSeat(t, "then run the tests")
		killedHook(spool) // taken and deleted; the print is the arrival below
		s.fold(obs{kind: "turn", text: "Done."})
		seen := time.Now().Add(time.Second)
		if early {
			s.fold(obs{kind: "user", text: "then run the tests"})
		}
		s.reportLeftOvers(seen)
		if !early {
			s.checkScreen(seen.Add(10 * time.Second))
			s.fold(obs{kind: "user", text: "then run the tests"})
		}
		for _, after := range []time.Duration{46, 120} {
			s.reportLeftOvers(seen.Add(after * time.Second))
			s.checkScreen(seen.Add(after * time.Second))
		}
		got := deliveries(drain(s.events))
		if len(got) != 1 || !got[0].OK || got[0].SendID != "r1" {
			t.Fatalf("early %v: deliveries %+v, want r1 confirmed once", early, got)
		}
		if len(h.typed) != 0 || len(h.keys) != 0 {
			t.Fatalf("typed %q, pressed %q", h.typed, h.keys)
		}
	}
}

// A held message still in the spool when the turn ends goes as before: the
// left-over sweep reports it not sent, once, and nothing else follows.
func TestBSeatHeldMessageLeftInTheSpoolIsReportedOnceAsLeftOver(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	s, h, spool := heldClaudeSeat(t, "then run the tests")
	s.fold(obs{kind: "aborted"})
	drain(s.events)
	seen := time.Now().Add(time.Second)
	for _, after := range []time.Duration{0, 46, 120} {
		s.reportLeftOvers(seen.Add(after * time.Second))
		s.checkScreen(seen.Add(after * time.Second))
	}
	got := deliveries(drain(s.events))
	if len(got) != 1 || got[0].SendID != "r1" || got[0].Outcome != OutcomeNotSent {
		t.Fatalf("deliveries %+v, want r1 not sent once", got)
	}
	if names, _ := filepath.Glob(filepath.Join(spool, "*")); len(names) != 0 || len(h.typed) != 0 || len(h.keys) != 0 {
		t.Fatalf("spool %v, typed %q, pressed %q", names, h.typed, h.keys)
	}
}

// Two after-turn sends queued in Codex's own queue during turn 1. Codex runs
// the first as turn 2; the second still waits in its queue for turn 2's end.
// Turn 2 goes quiet (a long build) and the seat calls it blocked. The second
// send must not be reported unconfirmed while turn 2 still runs and it is
// still queued.
func TestBSeatAMessageInTheAgentsOwnQueueIsNotReportedWhileItWaits(t *testing.T) {
	h := &fakeHost{alive: true, screen: "› Ask Codex to do anything\n  esc to interrupt\n"}
	s := newSeat(Spec{Name: "codex"}, codexAgent{}, h, "x", "")
	s.state = "working"
	s.trackHeld("a", "first queued message here", "after_turn", false)
	s.trackHeld("b", "second queued message here", "after_turn", false)
	s.fold(obs{kind: "turn", text: "Done."})
	s.fold(obs{kind: "user", text: "first queued message here"})
	s.fold(obs{kind: "working"})
	drain(s.events)
	base := time.Now()
	s.checkScreen(base.Add(11 * time.Minute)) // quiet turn 2: blocked
	s.checkScreen(base.Add(11*time.Minute + 20*time.Second))
	for _, ev := range deliveries(drain(s.events)) {
		if ev.SendID == "b" {
			t.Fatalf("b reported while turn 2 runs and b still waits in Codex's queue: %+v", ev)
		}
	}
}
