package relay

// The sixteenth review's seventh pass over the seat (sr7seat).

import (
	"path/filepath"
	"slices"
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

// SR7-3. The fifth review's "waited for good", for a block that turns
// stuck after it began. A model change asked while Codex works waits; the
// harness looks at it again only when the seat says blocked or exited
// (KindState), quiet (KindQuiet), or a send failed. Codex goes quiet for
// ten minutes with its spinner still up (a long build): blocked, not stuck,
// so the change keeps waiting, as it should. Then the spinner stops and the
// prompt does not come back: checkScreen sets stuck at that look
// (TestK9SeatSaysWhenABlockedAgentIsStuck), but says nothing. No event goes
// out, so applyWaitingRepick never runs again and the change waits for
// good on an agent the seat knows is stuck, though one asked now would
// restart it at once (heldBack).
func TestSr7SeatABlockThatTurnsStuckSaysSo(t *testing.T) {
	saved := screenEvery
	screenEvery = 2 * piLook
	t.Cleanup(func() { screenEvery = saved })
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeLines(t, path)
	busy := "• Running go test ./... (12m 03s • esc to interrupt)\n› "
	h := &fakeHost{alive: true, screen: busy}
	s := newSeat(Spec{}, codexAgent{}, h, "sr7-later-stuck", "thread-1")
	long := time.Now().Add(-11 * time.Minute)
	s.transcript, s.state, s.stateAt, s.lastLine, s.poll = path, "working", long, long, piLook
	go s.watch()
	t.Cleanup(s.Close)
	blocked := false
	deadline := time.After(5 * time.Second)
	for !blocked {
		select {
		case ev := <-s.events:
			blocked = ev.Kind == KindState && ev.State == "blocked"
		case <-deadline:
			t.Fatal("never blocked")
		}
	}
	if st := s.Snapshot(); st.Stuck {
		t.Fatal("stuck with its spinner up")
	}
	h.mu.Lock()
	h.screen = "$ "
	h.mu.Unlock()
	if !within(5*time.Second, func() bool { return s.Snapshot().Stuck }) {
		t.Fatal("never stuck after the spinner stopped")
	}
	var said []string
	wait := time.After(looks(40))
	for {
		select {
		case ev := <-s.events:
			said = append(said, ev.Kind)
			if ev.Kind == KindState || ev.Kind == KindQuiet {
				return
			}
			continue
		case <-wait:
		}
		break
	}
	t.Fatalf("the seat is stuck now (Status.Stuck) and said nothing a waiting change looks at (events %v): a model change that waited for the turn waits for good", said)
}

// SR7-2. SR6-2 for pi. pi's /compact runs no agent run: the extension
// saves "working" only at agent_start, so pi's status file says idle all
// through a compaction (the extension's own comment: a hand-over "pi
// refused outright (it was compacting)" was tried because the status said
// idle), and pi writes nothing to its session file until the compaction
// entry. The command's hold ends quietMoveLooks looks after the first idle
// look, and atRest for pi is the status file alone (idle, no dialog), so
// two looks later the seat says quiet while pi is still compacting. A
// model change waiting for quiet ("acting on one of its own commands; it
// starts again with X once it has") then kills pi mid-compaction: the
// /compact recorded delivered never takes effect. Claude's compaction is
// held by its spinner (TestSr6SeatClaudeCompactIsQuietWhileItRuns).
func TestSr7SeatPiCompactIsQuietWhileItRuns(t *testing.T) {
	isolatePi(t)
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	now := time.Now().Add(-time.Minute)
	lines := append([]string{piHeaderLine("first", now)}, piTurnLines("t1", "sort the shelves", "Sorted.", now.Add(time.Second))...)
	w := newPiSwitchSeat(t, time.Time{}, lines, entries(0))
	s := w.s
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("never quiet at the start: %q", got)
	}
	if _, err := s.Send("c1", "/compact", "queue"); err != nil {
		t.Fatal(err)
	}
	// pi compacts: its status file still says idle (no agent_start), and
	// nothing is written to the session until the compaction entry. The
	// extension says it compacts (session_before_compact), as pi starts.
	writePiStatus(t, w.status, piStatus{State: "idle", Compacting: true, SessionFile: w.first, SessionID: "first", SessionEntries: entries(0)})
	got := quietOrder(t, s, looks(16))
	if slices.Contains(got, "quiet") {
		t.Fatalf("events %q: quiet while pi is still compacting (no compaction entry in its session yet)", got)
	}
}
