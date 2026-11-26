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

const r10Earlier = "that message went as written (r1) to the agent that held the name codex before, which was stopped; Codex, running now, was not sent it as its own message (its hand-over brief may quote it); to give it to this one, send the words with relay_send"

// Finding 9. A send typed into the Codex that was stopped, whose arrival
// that Codex never confirmed (still waiting, or given up on), was that
// Codex's: the refusal says so, not that it is on its way to the Codex
// started since, which was never sent it.
func TestR10ASendOfAStoppedHolderIsNotOnItsWayToTheNewOne(t *testing.T) {
	for _, outcome := range []string{"", event.DeliveryUnconfirmed} {
		rec := &relayRecord{t: time.Now()}
		rec.start("codex", "codex")
		seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
		rec.send("codex", "r1", seq)
		if outcome != "" {
			rec.failed("codex", "r1", outcome)
		}
		rec.add(event.RelayEnd, event.RelayEndData{Name: "codex", Reason: "stopped"})
		rec.start("codex", "codex")
		st := state.Replay(rec.evs)
		if n := st.Relays["codex"].Awaited(); n != 0 {
			t.Fatalf("the new Codex awaits %d sends", n)
		}
		if id, why := forwardedAs(st, "codex", seq, ""); id != "r1" || why != r10Earlier {
			t.Errorf("outcome %q: refused as %s:\n %q\nwant r1:\n %q", outcome, id, why, r10Earlier)
		}
	}
}

// Finding 7. Codex is stopped, a fresh Codex is started, and the user's
// instruction is forwarded while it starts: the new Codex is typed it and
// confirms it. A switch to Claude then hands that instruction over in the
// brief, as the new Codex's own.
func TestR10AnInstructionHeldForAFreshStartIsInTheNextBrief(t *testing.T) {
	const text = "build the payment webhook handler"
	fresh, claude := newFakeSeat("two"), newFakeSeat("three")
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), fresh, claude}, nil)
	rt := newLaunchRuntime(t)
	tool := func(name string, args map[string]any) (string, bool) {
		return rt.relayTool(name, argStr(args), argNum(args), argList(args), false)
	}
	if out, isErr := tool("relay_control", map[string]any{"relay": "codex", "action": "stop"}); isErr {
		t.Fatalf("stop: %s", out)
	}
	started := make(chan string, 1)
	go func() {
		out, _ := tool("relay_start", map[string]any{"profile": "codex"})
		started <- out
	}()
	g.waitEntered(t)
	var seq int64
	rt.sync(func() { seq = rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text})).Seq })
	if out, isErr := tool("relay_forward", map[string]any{"relay": "codex", "seq": int(seq)}); isErr || !strings.Contains(out, "is starting") {
		t.Fatalf("forward while the fresh Codex starts: %q", out)
	}
	id := lastSendID(rt)
	g.release <- struct{}{}
	if out := recv(t, started, "relay_start"); !strings.Contains(out, "Started.") {
		t.Fatalf("relay_start: %q", out)
	}
	waitFor(t, "the new Codex to confirm the instruction", func() bool {
		var o state.SendOutcome
		rt.sync(func() { o = rt.st.SendOutcome(id) })
		return o == state.SendArrived
	})
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	if out := recv(t, switched, "switch"); !strings.Contains(out, "Handed over") {
		t.Fatalf("switch: %q", out)
	}
	if sent := claude.sentTexts(); len(sent) == 0 || !strings.Contains(sent[0], text) {
		t.Fatalf("Claude's brief leaves out the instruction:\n%q", sent)
	}
}

// Finding 9, its bound. A send passed back to the name after the earlier
// holder was stopped is the agent's that holds it now: on its way to it.
func TestR10ASendMovedOntoTheNewHolderIsItsOwn(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	r10switch(rec, "codex", "claude")
	r10moved(rec, "claude", "codex", "r1", seq)
	r10switch(rec, "claude", "codex")
	r10moved(rec, "codex", "claude", "r1", seq)
	if id, why := forwardedAs(state.Replay(rec.evs), "codex", seq, ""); id != "r1" || !strings.Contains(why, "already on its way to Codex") {
		t.Errorf("refused as %s: %q, want r1 as on its way", id, why)
	}
}
