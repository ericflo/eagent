package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// Finding 10. A send that waited for a switch from Claude to Codex and
// then certainly did not go to Codex did not go anywhere: Claude's list
// says so, and the user's message may be forwarded to Claude again.
func TestR14AMovedSendThatDidNotGoIsNotSent(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("claude", "claude")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("claude", "r1", seq)
	r10switch(rec, "claude", "codex")
	r10moved(rec, "codex", "claude", "r1", seq)
	rec.failed("codex", "r1", event.DeliveryNotSent)
	st := state.Replay(rec.evs)
	if o := st.SendOutcome("r1"); o != state.SendNotSent {
		t.Fatalf("r1: %q, want not_sent", o)
	}
	if id, why := forwardedAs(st, "claude", seq, ""); id != "" {
		t.Errorf("forwarding again to claude refused as %s: %q", id, why)
	}
	if sent := userSends(st, "claude", 8); len(sent) != 1 || !strings.Contains(sent[0], "did not go") {
		t.Errorf("claude's list: %q, want r1 once, as not sent", sent)
	}
	if sent := userSends(st, "codex", 8); len(sent) != 1 || !strings.Contains(sent[0], "did not go") {
		t.Errorf("codex's list: %q, want r1 once, as not sent", sent)
	}
}
