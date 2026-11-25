package harness

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// movedEvents are the relay.moved events on the record.
func movedEvents(rt *Runtime) []event.RelayMovedData {
	var out []event.RelayMovedData
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelayMovedData
			if ev.Type == event.RelayMoved && ev.Decode(&d) == nil {
				out = append(out, d)
			}
		}
	})
	return out
}

// A user message sent while a switch launches is moved to the new agent
// once (relay.moved codex -> claude). If the new agent is then restarted
// (a /model change, relay_control restart) before that message was typed,
// the message is still the new relay's: the restart must not record a
// second move "from codex", nor show the model the hand-over notice twice.
func TestASendMovedToANewAgentIsNotMovedAgainWhenThatAgentRestarts(t *testing.T) {
	// codex (old), claude (holds the brief until killed), claude restarted.
	brief := newClosingSeat("brief")
	again := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{&lingerSeat{newFakeSeat("one")}, brief, again}, nil)
	rt := newLaunchRuntime(t)
	sw := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	userSays(t, rt, "A: use the staging database")
	id := lastSendID(rt)
	g.release <- struct{}{}
	brief.waitEntered(t, "You are taking over")
	if m := movedEvents(rt); len(m) != 1 {
		t.Fatalf("after the switch: %d moves, want 1: %+v", len(m), m)
	}
	// The new agent is restarted while the brief is still waiting to go in.
	rs := relayToolAsync(rt, map[string]any{"action": "restart", "relay": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, rs, "restart")
	recv(t, sw, "switch")
	waitFor(t, "brief and A typed into the restarted agent", func() bool { return len(again.sentTexts()) == 2 })
	var forA []event.RelayMovedData
	for _, m := range movedEvents(rt) {
		if m.ID == id {
			forA = append(forA, m)
		}
	}
	if len(forA) != 1 {
		t.Errorf("A (%s) was moved %d times, want once: %+v", id, len(forA), forA)
	}
	rt.sync(func() {
		n := 0
		view := rt.st.View(nil)
		for _, m := range view {
			n += strings.Count(m.Text, "waited for the handover")
		}
		if n != 1 {
			t.Errorf("the model was told %d times that A waited for the handover, want once", n)
		}
		if rel := rt.st.Relays["claude"]; rel.Sends != 2 {
			t.Errorf("claude has %d sends on the record, want 2 (the brief and A)", rel.Sends)
		}
	})
}

// A switch made while the previous switch's handoff brief is still waiting
// to be typed (codex -> claude, then claude -> codex at once): the newest
// agent gets its own handoff brief and not the one written for claude, and
// the message A that waited for the first switch ends up on the record of
// the relay it went to, so the stopped one does not wait for it for ever.
func TestASwitchMadeWhileABriefWaitsTypesOnlyItsOwnBrief(t *testing.T) {
	brief := newClosingSeat("brief")
	back := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{&lingerSeat{newFakeSeat("one")}, brief, back}, nil)
	rt := newLaunchRuntime(t)
	sw1 := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	userSays(t, rt, "A: use the staging database")
	id := lastSendID(rt)
	g.release <- struct{}{}
	brief.waitEntered(t, "You are taking over")
	sw2 := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "codex", "relay": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, sw2, "second switch")
	recv(t, sw1, "first switch")
	waitFor(t, "everything typed into the last agent", func() bool { return len(back.sentTexts()) >= 2 })
	waitRelayStarts(t, rt)
	briefs := 0
	for _, s := range back.sentTexts() {
		if strings.HasPrefix(s, "You are taking over") {
			briefs++
		}
	}
	if briefs != 1 {
		t.Errorf("the last agent was typed %d handoff briefs, want 1: %q", briefs, firstWords(back.sentTexts()))
	}
	rt.sync(func() {
		if _, waiting := rt.st.Relays["claude"].Undelivered[id]; waiting {
			t.Errorf("claude (stopped) still waits for A (%s), which went to the new codex", id)
		}
	})
}
