package relay

import (
	"strings"
	"testing"
	"time"
)

// Codex's /new, /clear, /fork and /resume move it to another thread on a
// rollout of its own, and Codex says nothing of the move: typed, the seat
// stayed on the rollout it left, so every turn after was lost and every
// send reported unconfirmed (as on main). They are refused with the reason,
// nothing typed; any other command of Codex's is typed as before.
func TestK11CodexCommandsThatMoveItAreNotTyped(t *testing.T) {
	h := &fakeHost{alive: true, screen: "› \n"}
	s := newSeat(Spec{Name: "codex"}, codexAgent{}, h, "k11-codex", "")
	for _, cmd := range []string{"/new", "/clear", "/fork", "/resume", "/NEW", "/new the tables\n"} {
		_, err := s.Send("r1", cmd, "queue")
		if err == nil || !strings.Contains(err.Error(), "was not typed") || !strings.Contains(err.Error(), "stop this relay and start it again") {
			t.Errorf("%q: %v; want it refused with the reason", cmd, err)
		}
	}
	h.mu.Lock()
	typed, keys := len(h.typed), len(h.keys)
	h.mu.Unlock()
	if typed != 0 || keys != 0 {
		t.Fatalf("typed %d, keys %d; want nothing typed for a refused command", typed, keys)
	}
	if st := s.Snapshot(); len(st.Pending) != 0 {
		t.Fatalf("pending %v; want nothing waited on", st.Pending)
	}
	for _, c := range []struct {
		agent Agent
		text  string
	}{
		{codexAgent{}, "/compact"}, {codexAgent{}, "/newer"}, {codexAgent{}, "/new/path is the file"}, {claudeAgent{}, "/clear"}, {piAgent{}, "/new"},
	} {
		if cr, ok := c.agent.(commandRefuser); ok && isAgentCommand(c.text) && cr.refuses(strings.Fields(c.text)[0]) != "" {
			t.Errorf("%s refuses %q; want it typed", c.agent.Name(), c.text)
		}
	}
}

// A send that has waited 45 s is judged on the settled verdict of the
// agent's screen, never on one look: a Send that began meanwhile (or the
// agent writing, or a dialog answered) unsettles the verdict, and the look
// right after it, alone, reported a send unconfirmed though the agent was
// at work on a quiet turn and took it at its next step.
func TestK11AnUnsettledLookDecidesNothingOfASendThatWaited(t *testing.T) {
	now := time.Now()
	busy := "✻ Thinking… (12m · esc to interrupt)\n\n> \n  ? for shortcuts\n"
	s, h := hungSeat(busy, now.Add(-46*time.Second))
	s.checkScreen(now.Add(-3 * time.Second))
	s.checkScreen(now.Add(-1500 * time.Millisecond)) // settled: at work
	s.mu.Lock()
	s.saw, s.sawLast = "", "" // as another Send's start leaves it
	s.mu.Unlock()
	h.mu.Lock()
	h.screen = claudePromptScreen // one capture without the busy line
	h.mu.Unlock()
	s.checkScreen(now)
	if got := deliveredEvents(s); len(got) != 0 || len(s.Snapshot().Pending) != 1 {
		t.Fatalf("one unsettled look reported %+v, pending %v; want the send still watched", got, s.Snapshot().Pending)
	}
	h.mu.Lock()
	h.screen = busy
	h.mu.Unlock()
	s.checkScreen(now.Add(1500 * time.Millisecond))
	s.checkScreen(now.Add(3 * time.Second))
	if got := deliveredEvents(s); len(got) != 0 || len(s.Snapshot().Pending) != 1 {
		t.Fatalf("at work again: reported %+v, pending %v; want the send still watched", got, s.Snapshot().Pending)
	}
}
