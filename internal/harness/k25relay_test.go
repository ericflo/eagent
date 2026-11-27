package harness

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// The user's message is typed into Codex (not yet confirmed); eagent
// switches to Claude, and the handoff brief, the one thing that carries
// the user's message on, cannot be typed (not_sent). A switch leaves the
// user's messages unsettled on the old agent's record because its brief
// quotes them. This brief carried nothing, so the next settle (a resume, a
// stop from outside) settles the message as at a stop. (It was kept
// unsettled for good.)
func TestK25ASwitchWhoseBriefWasNotSentSettlesTheUsersMessage(t *testing.T) {
	seat := newK2FeedSeat(t)
	next := newFakeSeat("two")
	next.sendErr = errors.New("the agent is not running")
	g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	seat.waitGone(t)
	seat.end()
	g.release <- struct{}{}
	text := recv(t, switched, "switch")
	if !strings.Contains(text, "could not be typed") {
		t.Fatalf("switch: %q; want the brief not typed", text)
	}
	k1QueuesIdle(t, rt)
	var brief string
	var keep []string
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelaySendData
			if ev.Type == event.RelaySend && ev.Decode(&d) == nil && d.Name == "claude" && d.From == "eagent" {
				brief = d.ID
			}
		}
		_, keep = EndedWaiting(rt.st)
	})
	if got := deliveredFor(rt, brief); len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
		t.Fatalf("brief %s: %+v; want not_sent", brief, got)
	}
	if got := deliveredFor(rt, id); len(got) == 0 && slices.Contains(keep, id) {
		t.Fatalf("the user's message %s, whose only carrier (the brief) was not sent: no outcome on the record, and EndedWaiting keeps it unsettled for good (keep %v)", id, keep)
	}
	// What a resume, or a stop from outside the run, does with the record.
	rt.sync(func() { rt.settleEnded() })
	if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed {
		t.Fatalf("the user's message %s after the settle: %+v; want its one outcome, unconfirmed", id, got)
	}
}

// The same start, and a second switch (to pi) before Claude was typed its
// brief: Claude's brief is recorded not sent, and pi's brief hands over
// from Codex through Claude, quoting the user's message. That brief
// carried it: a later settle leaves it as the switch left it, with no
// outcome of its own to answer by sending it again.
func TestK25AQuickSecondSwitchsBriefCarriesTheUsersMessage(t *testing.T) {
	seat := newK2FeedSeat(t)
	stuck, final := newClosingSeat("stuck"), newFakeSeat("done")
	g := useGatedLaunches(t, []relaySeat{seat, stuck, final}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	first := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	seat.waitGone(t)
	seat.end()
	g.release <- struct{}{}
	stuck.waitEntered(t, "You are taking over") // Claude is on its startup screens: its brief is never typed
	second := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "pi", "relay": "claude"})
	g.release <- struct{}{}
	if text := recv(t, first, "switch"); !strings.Contains(text, "could not be typed") {
		t.Fatalf("the first switch: %q; want its brief not typed", text)
	}
	if text := recv(t, second, "switch"); !strings.Contains(text, "Handed over") {
		t.Fatalf("the second switch: %q", text)
	}
	waitFor(t, "pi typed its brief", func() bool { return len(final.sentTexts()) >= 1 })
	k1QueuesIdle(t, rt)
	if brief := final.sentTexts()[0]; !strings.Contains(brief, "run the tests") {
		t.Fatalf("pi's brief does not quote the user's message:\n%s", brief)
	}
	var briefs []string
	var keep []string
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelaySendData
			if ev.Type == event.RelaySend && ev.Decode(&d) == nil && d.From == "eagent" && strings.HasPrefix(d.Text, "You are taking over") {
				briefs = append(briefs, d.ID)
			}
		}
		_, keep = EndedWaiting(rt.st)
	})
	if len(briefs) != 2 {
		t.Fatalf("briefs on the record: %v; want Claude's and pi's", briefs)
	}
	if got := deliveredFor(rt, briefs[0]); len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
		t.Fatalf("Claude's brief %s: %+v; want not_sent", briefs[0], got)
	}
	if got := deliveredFor(rt, briefs[1]); len(got) == 1 && got[0].Outcome == event.DeliveryNotSent {
		t.Fatalf("pi's brief %s: %+v; want it typed", briefs[1], got)
	}
	if !slices.Contains(keep, id) {
		t.Fatalf("the user's message %s, which pi's brief quotes, is not kept as the switch left it (keep %v)", id, keep)
	}
	rt.sync(func() { rt.settleEnded() })
	if got := deliveredFor(rt, id); len(got) != 0 {
		t.Fatalf("the user's message %s after the settle: %+v; want no outcome (pi's brief carried it)", id, got)
	}
}

// The user's message is held for the end of Codex's turn (in the spool).
// The user quits while a start or stop of another agent is still under
// way, so finish waits for it after it has ended the loop's queue and
// before it stops watching the agents. In that window Codex's turn ends
// and its seat, still in use, takes the message back from the spool and
// says it was not sent. The queue takes no more work: the word is kept and
// written by finish. (It was dropped, the message was gone from the spool,
// and a resume called it unconfirmed.)
func TestK25ASeatsNotSentAfterTheQueueEndedIsRecorded(t *testing.T) {
	seat := newK2FeedSeat(t)
	rt, _, done := r10Session(t, seat)
	_, id := k2Forward(t, rt, seat, "and update the docs", "after_turn")

	rt.relayOps.Add(1) // a start or stop of another agent still under way
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	waitFor(t, "the loop's queue ended", func() bool {
		rt.queueMu.RLock()
		defer rt.queueMu.RUnlock()
		return rt.queueEnded
	})
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Outcome: relay.OutcomeNotSent,
		Detail: "Codex's turn ended before it took this message, so it was not delivered; send it again if it is still wanted"})
	seat.say(relay.Event{Kind: relay.KindActivity, Text: "idle"})
	rt.relayOps.Done()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	var got []event.RelayDeliveredData
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id {
			got = append(got, d)
		}
	}
	if len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
		t.Fatalf("the send %s that the seat took back from the spool and said not sent: outcomes on the record %+v; want one, not_sent", id, got)
	}
}
