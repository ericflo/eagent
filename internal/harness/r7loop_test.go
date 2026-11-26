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

// Finding 24: a closing word that is only a leaked control token says
// nothing, so it is not taken as said: send_message is refused, plain text
// is sent back as a reply with no tool call, and the session does not end
// in silence.
func TestR7AClosingWordOfOnlyControlTokensIsNotTakenAsSaid(t *testing.T) {
	const report = "Final: #42 and #44 are merged; #45 is not."
	t.Run("send_message", func(t *testing.T) {
		s, _, code := audit2Closing(t, func(msgs []map[string]any) reply {
			if strings.Contains(lastText(msgs), "text is required") {
				return say(report)
			}
			return say("<|im_end|>")
		})
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if said := s.ui.said(); len(said) == 0 || said[len(said)-1] != report {
			t.Fatalf("said %q", said)
		}
	})
	t.Run("plain text", func(t *testing.T) {
		s, _, code := audit2Closing(t, func(msgs []map[string]any) reply { return reply{text: "<|im_end|>"} })
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if n := audit2CountLast(s.model, "That reply had no tool call"); n != 1 {
			t.Fatalf("the empty report was sent back %d times, want 1", n)
		}
		// The earlier message was the last thing said before the closing
		// look; something must come after it.
		if said := s.ui.said(); len(said) < 2 {
			t.Fatalf("said %q", said)
		}
	})
}
