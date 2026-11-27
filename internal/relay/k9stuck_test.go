package relay

import (
	"testing"
	"time"
)

// The sixth review's rule: a model change that waited applies at a block
// only when the seat knows the agent is stuck (Status.Stuck): a start that
// never got ready, or a turn gone quiet whose screen does not show it busy.
// A quiet agent at work (a ten-minute build: its spinner still runs) is
// blocked but not stuck, and turns stuck once its screen stops saying so.
func TestK9SeatSaysWhenABlockedAgentIsStuck(t *testing.T) {
	busy := "• Running go test ./... (12m 03s • esc to interrupt)\n› "
	idle := "› Ask Codex to do anything"
	look := func(state string, ago time.Duration, screen string) *Seat {
		h := &fakeHost{alive: true, screen: screen}
		s := newSeat(Spec{}, codexAgent{}, h, "k9-stuck", "")
		s.state, s.stateAt = state, time.Now().Add(-ago)
		s.checkScreen(time.Now())
		return s
	}
	if st := look("working", 11*time.Minute, busy).Snapshot(); st.State != "blocked" || st.Stuck {
		t.Errorf("a quiet turn whose spinner runs: %s, stuck %v; want blocked, not stuck", st.State, st.Stuck)
	}
	if st := look("working", 11*time.Minute, "$ "+"\n").Snapshot(); st.State != "blocked" || !st.Stuck {
		t.Errorf("a quiet turn with nothing at work on its screen: %s, stuck %v; want blocked and stuck", st.State, st.Stuck)
	}
	if st := look("starting", 30*time.Second, "Loading…").Snapshot(); st.State != "blocked" || !st.Stuck {
		t.Errorf("a start not ready: %s, stuck %v; want blocked and stuck", st.State, st.Stuck)
	}
	// The build's spinner stops, the prompt not back: stuck from that look.
	h := &fakeHost{alive: true, screen: busy}
	s := newSeat(Spec{}, codexAgent{}, h, "k9-stuck-later", "")
	s.state, s.stateAt = "working", time.Now().Add(-11*time.Minute)
	s.checkScreen(time.Now())
	h.mu.Lock()
	h.screen = "$ "
	h.mu.Unlock()
	s.checkScreen(time.Now())
	s.checkScreen(time.Now()) // held two checks in a row
	if st := s.Snapshot(); st.State != "blocked" || !st.Stuck {
		t.Errorf("after the spinner stopped: %s, stuck %v; want blocked and stuck", st.State, st.Stuck)
	}
	// Back at its prompt: idle, nothing stuck.
	h.mu.Lock()
	h.screen = idle
	h.mu.Unlock()
	s.checkScreen(time.Now())
	if st := s.Snapshot(); st.State != "idle" || st.Stuck {
		t.Errorf("at its prompt: %s, stuck %v; want idle", st.State, st.Stuck)
	}
	_ = drain(s.events)
}
