package harness

import (
	"strings"
	"sync"
	"testing"
)

// Finding 21: a reply with no tool call and no words (a provider's error or
// refusal stop) is sent back once like any other reply that reaches no one,
// so the agent's report still reaches the user.
func TestR7AnEmptyReplyIsSentBackOnce(t *testing.T) {
	var mu sync.Mutex
	reminded := 0
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(tail, "had no tool call"):
			reminded++
			return say("Codex merged the PRs.")
		case strings.Contains(tail, "finished a turn"):
			return reply{}
		}
		return hold()
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	mu.Lock()
	defer mu.Unlock()
	if reminded != 1 {
		t.Fatalf("reminded %d times, want 1", reminded)
	}
}
