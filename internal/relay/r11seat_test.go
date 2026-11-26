package relay

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Finding 7: a seat closed during the send's last look at the screen (a
// switch, stop or restart) holds nothing for after the turn: no spool file
// for Claude or pi, no paste into Antigravity. The send says ErrClosed, so
// the harness hands it to the agent that took over.
func TestR11AnAfterTurnSendToASeatClosedDuringItsLastLookHoldsNothing(t *testing.T) {
	t.Parallel()
	for _, agent := range []Agent{claudeAgent{}, piAgent{name: "pi", label: "Pi", binary: "pi"}, antigravityAgent{}} {
		spool := filepath.Join(t.TempDir(), "after_turn")
		spec := Spec{Name: "x"}
		if _, ok := agent.(antigravityAgent); !ok {
			spec.Spool, spec.Hook = spool, "eagent relay-hook"
		}
		fh := &fakeHost{alive: true}
		h := &closingHost{fakeHost: fh}
		s := newSeat(spec, agent, h, "x", "")
		h.seat = s
		s.state = "working"
		close(s.done)
		msg, err := s.Send("r1", "then run the tests", "after_turn")
		if !errors.Is(err, ErrClosed) {
			t.Errorf("%T: Send returned %q, %v; want ErrClosed", agent, msg, err)
		}
		fh.mu.Lock()
		typed := fh.typed
		fh.mu.Unlock()
		if names := spoolNames(spool); len(names) != 0 || len(typed) != 0 || len(s.Snapshot().Pending) != 0 {
			t.Errorf("%T: spool %v, typed %q, pending %v", agent, names, typed, s.Snapshot().Pending)
		}
	}
}

// Finding 9: Claude's Stop hook claimed a held message (.txt.taken) and
// died before it removed the file or handed the text over. Once the turn
// it waited for is over and the hook had its chance, the message is lost:
// reported unconfirmed after the usual wait, once, never typed or pressed,
// and the stale file is gone.
func TestR11AHeldSendWhoseHookDiedAfterClaimingItIsReported(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	s, h, spool := heldClaudeSeat(t, "then run the tests")
	if taken, _ := takeSpool(spool); len(taken) != 1 { // the hook's rename, then it dies
		t.Fatalf("claimed %v", taken)
	}
	s.fold(obs{kind: "turn", text: "Done."})
	drain(s.events)
	seen := time.Now().Add(20 * time.Second)
	for _, after := range []time.Duration{0, 46, 120} {
		s.reportLeftOvers(seen.Add(after * time.Second))
		s.checkScreen(seen.Add(after * time.Second))
	}
	got := deliveries(drain(s.events))
	if len(got) != 1 || got[0].SendID != "r1" || got[0].OK || got[0].Outcome != OutcomeUnconfirmed {
		t.Fatalf("deliveries %+v, want r1 unconfirmed once (pending %v)", got, s.Snapshot().Pending)
	}
	if names := spoolNames(spool); len(h.typed) != 0 || len(h.keys) != 0 || len(names) != 0 {
		t.Fatalf("typed %q, pressed %q, spool %v", h.typed, h.keys, names)
	}
}
