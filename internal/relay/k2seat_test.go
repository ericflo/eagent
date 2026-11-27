package relay

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A12: a held message pi's extension took out of the spool and never ran
// (an interrupt put it back in pi's editor) is lost once the turn is over,
// also when a pi dialog opened within the grace after that turn end: the
// dialog holds the seat while it is up, and the turn end stands after it.
// Lost, it is reported unconfirmed after the usual wait; never typed again.
func TestK2APiDialogAfterATurnEndKeepsThatTurnEnd(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "pi", Spool: spool}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "k2-a12", "")
	s.state = "working"
	if _, err := s.Send("r1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.fold(obs{kind: "aborted"})
	s.reportDialog("Allow rm -rf build?") // opens within the grace
	names := spoolNames(spool)
	for _, n := range names {
		if err := os.Remove(filepath.Join(spool, n)); err != nil { // the extension removed it
			t.Fatal(err)
		}
	}
	s.reportDialog("")
	now := time.Now()
	s.leftOvers(now.Add(heldGrace), false)
	s.mu.Lock()
	lost := len(s.pending) == 1 && s.pending[0].lost
	s.stateAt = now.Add(-time.Hour)
	s.mu.Unlock()
	if !lost {
		t.Fatalf("the held message whose file is gone is not lost (pending %v, spool had %v)", s.Snapshot().Pending, names)
	}
	s.checkScreen(now.Add(time.Hour))
	var got []string
	for _, ev := range deliveries(drain(s.events)) {
		got = append(got, ev.SendID+":"+ev.Outcome)
	}
	if !slices.Equal(got, []string{"r1:" + OutcomeUnconfirmed}) || len(k1Typed(h)) != 0 {
		t.Fatalf("reported %v, typed %q; want r1 unconfirmed, nothing typed", got, k1Typed(h))
	}
}
