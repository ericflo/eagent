package relay

import (
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
