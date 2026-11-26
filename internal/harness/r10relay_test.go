package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// r10moved is a send that waited for a switch from prev and went to name.
func r10moved(rec *relayRecord, name, prev, id string, seq int64) {
	rec.add(event.RelayMoved, event.RelayMovedData{RelaySendData: event.RelaySendData{Name: name, ID: id, From: "user", Seq: seq, Mode: "queue", Text: "check the version"}, Prev: prev})
}

// r10switch records a switch from one relay to another, as finishLaunch
// does: the old relay's end, then the new one's start.
func r10switch(rec *relayRecord, from, to string) {
	rec.add(event.RelayEnd, event.RelayEndData{Name: from, Reason: "handed over to " + to})
	rec.start(to, to)
}

// Finding 6. A send that waited through a switch from Codex to Claude, and
// through the switch back, is Codex's again, listed once. When it then
// certainly did not go, the user's message may be forwarded to Codex again,
// and nothing claims it went to Claude or to the Codex that was stopped.
func TestR10ASendMovedBackToItsFirstRelayIsThatRelaysAgain(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	r10switch(rec, "codex", "claude")
	r10moved(rec, "claude", "codex", "r1", seq)
	r10switch(rec, "claude", "codex")
	r10moved(rec, "codex", "claude", "r1", seq)
	rec.failed("codex", "r1", event.DeliveryNotSent)
	st := state.Replay(rec.evs)
	if id, why := forwardedAs(st, "codex", seq, ""); id != "" {
		t.Errorf("forwarding again to codex refused as %s: %q", id, why)
	}
	if sent := userSends(st, "codex", 8); len(sent) != 1 || !strings.Contains(sent[0], "did not go") {
		t.Errorf("codex's list: %q, want r1 once, as not sent", sent)
	}
	if sent := userSends(st, "claude", 8); len(sent) != 1 || !strings.Contains(sent[0], "went to Codex") {
		t.Errorf("claude's list: %q, want r1 once, as gone to Codex", sent)
	}
}

// Finding 6, the claim. A send that went on to Claude and certainly did
// not go there says nothing of the Codex that held the name before: the
// refusal says where it went, not that the stopped Codex had it.
func TestR10ASendThatDidNotGoIsNotClaimedForTheEarlierHolder(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	r10switch(rec, "codex", "claude")
	r10moved(rec, "claude", "codex", "r1", seq)
	rec.failed("claude", "r1", event.DeliveryNotSent)
	r10switch(rec, "claude", "codex")
	id, why := forwardedAs(state.Replay(rec.evs), "codex", seq, "")
	if id != "r1" || strings.Contains(why, "held the name") || !strings.Contains(why, "went to Claude instead") {
		t.Errorf("refused as %s: %q, want r1 as gone to Claude", id, why)
	}
}
