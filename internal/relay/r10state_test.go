package relay

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// A reattach whose history shows the agent's last turn over, where the
// record's last state for the relay is not idle: eagent stopped while the
// record said starting (a restart or model change that resumes the agent's
// own conversation, stopped before the seat said idle), blocked (a slow
// start, "not ready after 25s"), or working (the turn ended between the
// seat's last read and the record's last relay event). The record's state
// changes only by the seat's state events, deduplicated as onRelayEvent
// does (same state, no detail, is dropped). After the catch-up the record
// must say what the seat says: idle.
func TestRV10AReattachWhoseHistoryEndsIdleCorrectsTheRecordedState(t *testing.T) {
	t.Parallel()
	since := time.Now().Add(-time.Minute)
	for k := 0; k < 6; k++ {
		i, recorded := k%3, recordedName(k%3)
		// The control (k >= 3): the seat as 59a2069 built it for a reattach
		// with nothing restored (catchingUp false), so the history's turns
		// go through setState.
		control := k >= 3
		lines := claudeTurnLines("sort the shelves", "Sorted.", since.Add(-10*time.Second))
		lines = append(lines, claudeTurnLines("now the chairs", "Counted.", since.Add(-3*time.Second))...)
		path := filepath.Join(t.TempDir(), "t.jsonl")
		writeLines(t, path, lines...)
		s := attachSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, fmt.Sprintf("rv10s-%d", k), "", path, since)
		s.poll = piLook
		if control {
			s.catchingUp = false
		}
		go s.watch()
		if !eventually(func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.catchingUp && !s.lastLine.IsZero() }) {
			s.Close()
			t.Fatalf("record %s: never caught up", recorded)
		}
		// A few more looks, so anything said at the catch-up is in.
		var said []string
		within(10*piLook, func() bool {
			for _, ev := range drain(s.events) {
				if ev.Kind == KindState {
					said = append(said, ev.State)
					if ev.State != recorded || ev.Detail != "" {
						recorded = ev.State
					}
				}
			}
			return false
		})
		seat := s.Snapshot().State
		s.Close()
		if recorded != seat {
			t.Errorf("record %s (control %v): the seat is %s but said states %v, so the record still says %s", recordedName(i), control, seat, said, recorded)
		}
	}
}

func recordedName(i int) string { return []string{"starting", "blocked", "working"}[i] }
