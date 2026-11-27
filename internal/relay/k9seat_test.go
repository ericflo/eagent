package relay

// The sixteenth review's seventh pass over the seat (sr7seat).

import (
	"path/filepath"
	"testing"
	"time"
)

// SR7-1. pi draws no busy line the seat reads (piAgent is no busyScreen):
// its own status file is what says it works (workReporter). A pi turn that
// runs one long tool (a fifteen-minute build) writes nothing to its session
// file meanwhile, so after ten minutes checkScreen marks the seat blocked,
// and sets Stuck from s.busy(screen) alone, which is always false for pi.
// The status file says "working" all along. Status.Stuck then says the seat
// knows pi is not working; heldBack lets a waiting model change through,
// and the harness restarts pi in the middle of its build: the turn it was
// running for the user's message is cut off.
func TestSr7SeatPiAtWorkInALongToolIsNotStuck(t *testing.T) {
	isolatePi(t)
	status := filepath.Join(t.TempDir(), "pi-status.json")
	writePiStatus(t, status, piStatus{State: "working", SessionFile: "/x.jsonl", SessionID: "id"})
	h := &fakeHost{alive: true, screen: piFooterScreen}
	s := newSeat(Spec{StatusFile: status}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "sr7-pi-stuck", "id")
	s.state, s.stateAt = "working", time.Now().Add(-11*time.Minute)
	s.checkScreen(time.Now())
	if st := s.Snapshot(); st.State != "blocked" || st.Stuck {
		t.Errorf("pi quiet in a turn its status file says is running: %s, stuck %v; want blocked, not stuck", st.State, st.Stuck)
	}
	// A later look at the block, the status file still saying working.
	s.checkScreen(time.Now())
	if st := s.Snapshot(); st.State != "blocked" || st.Stuck {
		t.Errorf("at the next look: %s, stuck %v; want blocked, not stuck", st.State, st.Stuck)
	}
	_ = drain(s.events)
}
