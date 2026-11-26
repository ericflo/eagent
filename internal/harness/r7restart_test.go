package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// r7TooLong is a provider's refusal of a request as too long.
var r7TooLong = reply{status: 400, body: `{"error":{"message":"prompt is too long for the model's context window","type":"invalid_request_error"}}`}

// A model whose first call of every conversation goes through but is sent
// back (plain text, no tool call), and whose second call, the reply and the
// correction appended, is refused as too long. Each conversation is
// "refused after its calls went through", so each restart once reset the
// count of restarts in a row and carried the same unseen message whole into
// the next: a paid dossier pass after another, without end. The restarts in
// a row are bounded, and the user is told.
func TestARefusalAfterASentBackCallDoesNotRestartWithoutEnd(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	brain := func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		if strings.HasPrefix(lastUserText(m), "[eagent] That reply had no tool call") {
			return r7TooLong
		}
		return reply{text: "let me think about that"}
	}
	s := startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 60000, 2048
	})
	r5LogOnFailure(s)
	s.settled()
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	r6Until(t, "the user told the conversation is full, or too many restarts", func() bool {
		return r6ToldFull(s) > 0 || r6Dossiers(s) > restartRescueMax+3
	})
	if n := r6Dossiers(s); n > restartRescueMax+2 {
		t.Fatalf("%d restarts in a row, each after a refusal on the second call; want at most %d", n, restartRescueMax+2)
	}
	if n := r5Told(s, fullNotice+": after several restarts in a row"); n != 1 {
		t.Fatalf("told %d times that the restarts stopped as the window was reached again, want once", n)
	}
}
