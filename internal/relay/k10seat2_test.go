package relay

import (
	"path/filepath"
	"testing"
	"time"
)

// SR9-3, compacting: a message typed into pi while it compacts waits until
// the compaction is over; pi's status says idle and compacting all that
// time. The 45-second check counts that as at work, as it does a busy
// screen, and keeps the send for its arrival.
func TestK10SendTypedWhilePiCompactsIsKeptPast45s(t *testing.T) {
	isolatePi(t)
	savedSettle := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = savedSettle })
	status := filepath.Join(t.TempDir(), "pi-status.json")
	writePiStatus(t, status, piStatus{State: "idle", Compacting: true})
	h := &fakeHost{alive: true, screen: piFooterScreen}
	s := newSeat(Spec{StatusFile: status}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "k10-pi-compact", "sess-1")
	s.state = "idle"
	if _, err := s.Send("q1", "also run the linter", "queue"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.checkScreen(now.Add(43500 * time.Millisecond)) // the screen checks before
	s.checkScreen(now.Add(46 * time.Second))
	if got := deliveredEvents(s); len(got) != 0 || len(s.Snapshot().Pending) != 1 {
		t.Fatalf("reported %+v, pending %v; want q1 kept while pi compacts", got, s.Snapshot().Pending)
	}
	writePiStatus(t, status, piStatus{State: "idle"})
	s.fold(obs{kind: "user", text: "also run the linter", at: time.Now()})
	if got := deliveredEvents(s); len(got) != 1 || !got[0].OK {
		t.Fatalf("outcomes %+v; want q1 delivered by its arrival after the compaction", got)
	}
}
