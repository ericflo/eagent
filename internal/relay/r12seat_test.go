package relay

import (
	"os"
	"slices"
	"testing"
	"time"
)

// claudeLoginScreen is a startup screen the seat knows no answer to: text
// and an Enter typed into it would pick a login method.
const claudeLoginScreen = " Select login method:\n\n ❯ 1. Claude account with subscription\n   2. Anthropic Console account\n"

// Finding 8: eagent stopped while the record said the relay was starting
// or blocked (a slow start, "not ready after 25s"), before the agent had
// written any transcript. The reattached seat has no history to read: the
// record's state stands, nothing is said, a send is not typed into the
// login screen, and the screen is looked at, so the state is settled from
// it once the agent's prompt shows.
func TestR12AReattachWithNothingInTheHistoryKeepsTheRecordedState(t *testing.T) {
	t.Parallel()
	since := time.Now().Add(-time.Minute)
	for _, recorded := range []string{"starting", "blocked"} {
		h := &fakeHost{alive: true, screen: claudeLoginScreen}
		s := attachSeat(Spec{State: recorded}, claudeAgent{}, h, "x", "", "", since)
		s.caughtUp(time.Now())
		for _, ev := range drain(s.events) {
			if ev.Kind == KindState {
				t.Errorf("record %s: said %s at the catch-up", recorded, ev.State)
			}
		}
		now := time.Now()
		if st := s.Snapshot().State; st != recorded || !s.wantsDialogCheck(now) {
			t.Fatalf("record %s: the seat is %s, looks at the screen %v", recorded, st, s.wantsDialogCheck(now))
		}
		if _, err := s.waitReady(-time.Second); err == nil {
			t.Errorf("record %s: a send would be typed into the login screen", recorded)
		}
		s.checkScreen(now)
		if len(h.typed) != 0 || len(h.keys) != 0 || s.Snapshot().State != recorded {
			t.Fatalf("record %s: typed %q, pressed %q, the seat is %s", recorded, h.typed, h.keys, s.Snapshot().State)
		}
		h.mu.Lock()
		h.screen = "> \n  ? for shortcuts\n"
		h.mu.Unlock()
		s.checkScreen(now.Add(2 * time.Second))
		s.checkScreen(now.Add(4 * time.Second))
		var said []string
		for _, ev := range drain(s.events) {
			if ev.Kind == KindState {
				said = append(said, ev.State)
			}
		}
		if len(said) != 1 || said[0] != "idle" || s.Snapshot().State != "idle" {
			t.Errorf("record %s: at the prompt said %v and the seat is %s; want idle", recorded, said, s.Snapshot().State)
		}
	}
}

// stateEvents is the states a seat said, in order.
func stateEvents(s *Seat) []string {
	var said []string
	for _, ev := range drain(s.events) {
		if ev.Kind == KindState {
			said = append(said, ev.State)
		}
	}
	return said
}

// Finding 9: pi went quiet for ten minutes in a turn (blocked, hung), then
// its extension reports a dialog, which is answered with no transcript
// line in between. pi is still in its turn: the seat goes back to working,
// not idle, whether pi's status says it works (closed) or says nothing
// any more (gone).
func TestR12APiDialogOverAQuietTurnGoesBackToWorking(t *testing.T) {
	t.Parallel()
	pi := piAgent{label: "Pi"}
	for _, end := range []string{"closed", "gone"} {
		s, status := piReadySeat(t, piFooterScreen, &piStatus{State: "working", SessionFile: "/x.jsonl", SessionID: "id"})
		now := time.Now()
		s.state, s.stateAt, s.lastLine = "working", now.Add(-11*time.Minute), now.Add(-11*time.Minute)
		s.checkScreen(now)
		if st := s.Snapshot().State; st != "blocked" || !s.hung {
			t.Fatalf("%s: not marked quiet: %s", end, st)
		}
		drain(s.events)
		writePiStatus(t, status, piStatus{State: "working", Prompt: "Allow rm -rf build?", SessionFile: "/x.jsonl", SessionID: "id"})
		s.reportDialog(pi.reportedDialog(s))
		if end == "closed" {
			writePiStatus(t, status, piStatus{State: "working", SessionFile: "/x.jsonl", SessionID: "id"})
		} else if err := os.Remove(status); err != nil {
			t.Fatal(err)
		}
		s.reportDialog(pi.reportedDialog(s))
		if said := stateEvents(s); !slices.Equal(said, []string{"blocked", "working"}) || s.Snapshot().State != "working" {
			t.Errorf("%s: said %v and the seat is %s; want blocked, working", end, said, s.Snapshot().State)
		}
	}
}

// Finding 9, the dialog over a seat blocked for another reason (here the
// record's blocked kept on a reattach with no turn in the history): when it
// closes and pi's status says pi works, the seat says working; when pi is
// idle, idle as before.
func TestR12APiDialogClosingOnAWorkingPiSaysWorking(t *testing.T) {
	t.Parallel()
	pi := piAgent{label: "Pi"}
	for _, after := range []string{"working", "idle"} {
		status := t.TempDir() + "/pi-status.json"
		writePiStatus(t, status, piStatus{State: "idle", Prompt: "Resume the goal?"})
		s := attachSeat(Spec{State: "blocked", StatusFile: status}, pi, &fakeHost{alive: true, screen: piFooterScreen}, "x", "", "", time.Now().Add(-time.Minute))
		s.caughtUp(time.Now())
		s.reportDialog(pi.reportedDialog(s))
		writePiStatus(t, status, piStatus{State: after})
		s.reportDialog(pi.reportedDialog(s))
		if said := stateEvents(s); !slices.Equal(said, []string{"blocked", after}) || s.Snapshot().State != after {
			t.Errorf("pi %s: said %v and the seat is %s; want blocked, %s", after, said, s.Snapshot().State, after)
		}
	}
}
