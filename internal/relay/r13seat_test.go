package relay

import (
	"testing"
	"time"
)

// deliveredEvents is the delivery events a seat said, in order.
func deliveredEvents(s *Seat) []Event {
	var out []Event
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered {
			out = append(out, ev)
		}
	}
	return out
}

// hungSeat is a Claude seat checked by hand (no watcher), blocked for
// going quiet in a turn (hung), with one send typed into it at.
func hungSeat(screen string, at time.Time) (*Seat, *fakeHost) {
	h := &fakeHost{alive: true, screen: screen}
	s := newSeat(Spec{}, claudeAgent{}, h, "r13s-hung", "")
	s.state, s.stateAt, s.hung = "blocked", at.Add(-11*time.Minute), true
	s.pending = []*pending{{id: "r1", text: "and the tables", mode: "queue", at: at}}
	return s, h
}

// Finding 15: a seat quiet for ten minutes in a turn (blocked, hung) takes
// a queued send while its screen shows it busy. No Enter goes to it while
// it is blocked: the agent is busy, or the screen is a dialog the seat does
// not know, and the Enter would answer it.
func TestR13SHungSeatPressesNoEnter(t *testing.T) {
	t.Parallel()
	for name, screen := range map[string]string{
		"busy":   "✻ Thinking… (12m · esc to interrupt)\n\n> and the tables\n  ? for shortcuts\n",
		"dialog": "Do you want to proceed?\n❯ 1. Yes\n  2. No\n\n> and the tables\n",
	} {
		now := time.Now()
		s, h := hungSeat(screen, now)
		s.checkScreen(now.Add(7 * time.Second))
		h.mu.Lock()
		keys := h.keys
		h.mu.Unlock()
		if len(keys) != 0 {
			t.Errorf("%s: pressed %q into a hung seat", name, keys)
		}
	}
}

// Finding 15: a hung seat whose screen (Claude) or status file (pi) says it
// still works reads a queued send at its next step, as a working one does:
// it is not reported unconfirmed, and stays watched.
func TestR13SHungWorkingSeatKeepsItsSend(t *testing.T) {
	t.Parallel()
	now := time.Now()
	claude, _ := hungSeat("✻ Thinking… (12m · esc to interrupt)\n\n> \n  ? for shortcuts\n", now.Add(-46*time.Second))
	pi, _ := piReadySeat(t, piFooterScreen, &piStatus{State: "working", SessionFile: "/x.jsonl", SessionID: "id"})
	pi.state, pi.stateAt, pi.hung = "blocked", now.Add(-11*time.Minute), true
	pi.pending = []*pending{{id: "r1", text: "and the tables", mode: "queue", at: now.Add(-46 * time.Second)}}
	for name, s := range map[string]*Seat{"claude": claude, "pi": pi} {
		s.checkScreen(now)
		if got := deliveredEvents(s); len(got) != 0 || len(s.Snapshot().Pending) != 1 {
			t.Errorf("%s: reported %+v, pending %v; want the send still watched", name, got, s.Snapshot().Pending)
		}
	}
}
