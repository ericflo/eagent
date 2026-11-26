package relay

import (
	"errors"
	"path/filepath"
	"testing"
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
