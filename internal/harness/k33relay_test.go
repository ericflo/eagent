package harness

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// k33Typed forwards the user's message to the working Codex and waits
// until it is typed; n is how many are typed by then.
func k33Typed(t *testing.T, rt *Runtime, seat *k2FeedSeat, text string, n int) string {
	t.Helper()
	rt.sync(func() {
		d := event.UserMessageData{Text: text, Delivery: "queue"}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("not routed: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
	waitFor(t, "the message typed", func() bool { return len(seat.sentTexts()) == n })
	k1QueuesIdle(t, rt)
	return lastSendID(rt)
}

// k33Switch switches from Codex to Claude and returns the brief typed
// into Claude.
func k33Switch(t *testing.T, rt *Runtime, g *gatedLaunches, seat *k2FeedSeat, next *fakeSeat) string {
	t.Helper()
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	seat.waitGone(t)
	seat.end()
	g.release <- struct{}{}
	recv(t, switched, "switch")
	k1QueuesIdle(t, rt)
	texts := next.sentTexts()
	if len(texts) == 0 {
		t.Fatal("no brief was typed")
	}
	return texts[0]
}

// The brief of a switch is the only way to the new agent for a message of
// the user's that the old one never showed it read, and the switch leaves
// such a message without an outcome on that account (the brief carried
// it). The brief quotes the latest 12 of the user's messages; with
// thirteen waiting, the oldest was quoted nowhere, reached nobody, and kept
// no outcome for good (main loses it the same way). Every message that
// waits is quoted, however many came after it.
func TestK33TheBriefCarriesEveryMessageThatWaits(t *testing.T) {
	seat := newK2FeedSeat(t)
	next := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
	rt := newLaunchRuntime(t)
	for i := range 13 {
		k33Typed(t, rt, seat, fmt.Sprintf("task %c please", 'A'+i), i+1)
	}
	brief := k33Switch(t, rt, g, seat, next)
	at := -1
	for i := range 13 {
		next := strings.Index(brief, fmt.Sprintf("- task %c please\n", 'A'+i))
		if next <= at {
			t.Fatalf("the brief does not quote the thirteen waiting messages, oldest first (task %c):\n%s", 'A'+i, brief)
		}
		at = next
	}
	if !strings.Contains(brief, "(the latest 12, and before them the earlier ones not yet shown as read)") {
		t.Fatalf("the brief does not say what it quotes:\n%s", brief)
	}
}

// Messages the old agent showed it read are history, which its session
// log holds in full: the brief still quotes the latest 12 of those, each up
// to 3000 bytes. One that waits is quoted whole, as the brief is all the
// new agent will see of it.
func TestK33AMessageThatWaitsIsQuotedWholeAndHistoryIsNot(t *testing.T) {
	seat := newK2FeedSeat(t)
	next := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
	rt := newLaunchRuntime(t)
	long := func(word string) string { return strings.Repeat(word+" ", 1500) + "END-" + word }
	for i := range 13 {
		text := fmt.Sprintf("task %c please", 'A'+i)
		if i == 0 {
			text = long("read")
		}
		id := k33Typed(t, rt, seat, text, i+1)
		seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
		waitFor(t, "the arrival recorded", func() bool { return k2Outcome(rt, id) == "arrived" })
	}
	k33Typed(t, rt, seat, long("waits"), 14)
	brief := k33Switch(t, rt, g, seat, next)
	if !strings.Contains(brief, "END-waits") {
		t.Fatalf("the waiting message is not quoted whole (%d bytes of brief)", len(brief))
	}
	if strings.Contains(brief, "read read") || strings.Contains(brief, "task B please") || !strings.Contains(brief, "task C please") || !strings.Contains(brief, "(the latest 12)\n") {
		t.Fatalf("history is not the latest 12 (the long first message and task B left out): %d bytes of brief, starting %q", len(brief), brief[:min(len(brief), 400)])
	}
}

// A long message the old agent read is clipped in the brief, as before.
func TestK33ALongMessageTheOldAgentReadIsClipped(t *testing.T) {
	seat := newK2FeedSeat(t)
	next := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
	rt := newLaunchRuntime(t)
	id := k33Typed(t, rt, seat, strings.Repeat("read ", 1500)+"END-read", 1)
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
	waitFor(t, "the arrival recorded", func() bool { return k2Outcome(rt, id) == "arrived" })
	if brief := k33Switch(t, rt, g, seat, next); strings.Contains(brief, "END-read") || !strings.Contains(brief, "read read") {
		t.Fatalf("a message the old agent read is not quoted clipped: %d bytes of brief", len(brief))
	}
}
