package relay

import (
	"testing"
	"time"
)

// A slow start: the agent's prompt first shows on the screen check that
// falls just past 25 s. That check has no verdict yet (the one before it saw
// the loading screen), and "decides nothing on an empty verdict" is not held
// by the 25 s branch: the seat says the agent is blocked and stuck, "still
// not ready after 25s", while the screen it quotes is the agent's prompt. A
// send that comes then is refused as if a dialog were up. On main the first
// ready look past 25 s took the ready branch and waited one more look.
func TestSR11SeatStartReadyAt25sIsCalledStuck(t *testing.T) {
	now := time.Now()
	h := &fakeHost{alive: true, screen: "Loading Claude Code…\n"}
	s := newSeat(Spec{}, claudeAgent{}, h, "sr11-slow", "")
	s.state, s.stateAt = "starting", now.Add(-24*time.Second)
	s.checkScreen(now) // 24 s: still loading
	h.mu.Lock()
	h.screen = claudePromptScreen // the prompt shows
	h.mu.Unlock()
	s.checkScreen(now.Add(1500 * time.Millisecond)) // 25.5 s: first check that sees it
	st := s.Snapshot()
	if st.State == "blocked" || st.Stuck {
		t.Errorf("the prompt shows, yet the seat says %s (stuck %v): a check with no verdict decided the start failed", st.State, st.Stuck)
	}
	for _, ev := range drain(s.events) {
		if ev.Kind == KindState && ev.State == "blocked" {
			t.Errorf("told the model %q while the screen shows the agent's prompt", ev.Detail)
		}
	}
	// The same after a startup dialog answered past 25 s (Claude's
	// bypass-permissions warning on a slow start): the answered check clears
	// what the seat saw, so the first check after it, at the prompt, has no
	// verdict, and the start is called stuck.
	h2 := &fakeHost{alive: true, screen: "Bypass Permissions mode\n❯ 1. No, exit\n  2. Yes, I accept\n"}
	s2 := newSeat(Spec{}, claudeAgent{}, h2, "sr11-slow2", "")
	s2.state, s2.stateAt = "starting", now.Add(-26*time.Second)
	s2.checkScreen(now) // the warning: answered
	h2.mu.Lock()
	h2.screen = claudePromptScreen
	h2.mu.Unlock()
	s2.checkScreen(now.Add(1500 * time.Millisecond))
	if st := s2.Snapshot(); st.State == "blocked" || st.Stuck {
		t.Errorf("after the startup dialog was answered, the prompt shows, yet the seat says %s (stuck %v)", st.State, st.Stuck)
	}
	readyWait = 200 * time.Millisecond
	defer func() { readyWait = 40 * time.Second }()
	if _, err := s.Send("m1", "hello", "queue"); err != nil && err.Error() != "the agent is still starting; check relay_screen" {
		t.Errorf("a send to an agent whose prompt shows was refused as if a dialog were up: %v", err)
	}
}
