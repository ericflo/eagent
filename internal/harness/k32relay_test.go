package harness

import (
	"errors"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// The user's message is typed into Codex and not confirmed; eagent switches
// to Claude, and the brief, which alone carried the message, cannot be
// typed. Codex is then started again, fresh, under its old name. A resume
// settles what no brief carried as at a stop (EndedWaiting), and the old
// Codex is still such an agent though a new one holds its name: the
// message gets its one outcome, unconfirmed. (Only the newest agent under
// a name was looked at, so the message kept no outcome for good.) What was
// sent to the new Codex is not the old one's, and stays.
func TestK32AMessageToAnAgentWhoseNameWasTakenAgainIsStillSettled(t *testing.T) {
	seat := newK2FeedSeat(t)
	next := newFakeSeat("two")
	next.sendErr = errors.New("the agent is not running")
	again := newFakeSeat("three")
	g := useGatedLaunches(t, []relaySeat{seat, next, again}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	seat.waitGone(t)
	seat.end()
	g.release <- struct{}{}
	if text := recv(t, switched, "switch"); !strings.Contains(text, "could not be typed") {
		t.Fatalf("switch: %q; want the brief not typed", text)
	}
	k1QueuesIdle(t, rt)
	g.release <- struct{}{}
	if out, _ := rt.relayTool("relay_start", argStr(map[string]any{"profile": "codex"}), argNum(nil), argList(nil), false); !strings.Contains(out, "codex") {
		t.Fatalf("relay_start: %q", out)
	}
	var own string
	rt.sync(func() {
		own = rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: "r90", From: "user", Mode: "queue", Text: "and the linter"})).Type
	})
	if got := deliveredFor(rt, id); len(got) != 0 || own == "" {
		t.Fatalf("setup: the user's message %s has %+v before the settle", id, got)
	}
	for range 2 { // a second settle writes nothing more
		rt.sync(func() { rt.settleEnded() })
	}
	if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed || !strings.Contains(got[0].Detail, "was stopped") {
		t.Fatalf("the user's message %s after the settle: %+v; want its one outcome, unconfirmed (no brief carried it, and its agent was stopped)", id, got)
	}
	if got := deliveredFor(rt, "r90"); len(got) != 0 {
		t.Fatalf("what was sent to the new Codex was settled with the old one's: %+v", got)
	}
	rt.sync(func() {
		if rels, _ := EndedWaiting(rt.st); len(rels) != 0 {
			t.Errorf("%d stopped agents are still waited on after the settle", len(rels))
		}
	})
}

// A seat taken out of use says how a send of its own went, and says it
// once on the record: the same word again writes nothing, as for a seat
// still in use while the session ends. A word that differs is its newer
// one, and is written.
func TestK32ARetiredSeatsWordIsWrittenOnce(t *testing.T) {
	seat := newK2FeedSeat(t)
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	word := func(ok bool, outcome string) {
		rt.sync(func() {
			rt.retiredNews("codex", seat, rt.st.Relays["codex"], relay.Event{Kind: relay.KindDelivered, SendID: id, OK: ok, Outcome: outcome, Detail: "as the seat saw it"})
		})
	}
	word(false, relay.OutcomeUnconfirmed)
	word(false, relay.OutcomeUnconfirmed)
	if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed {
		t.Fatalf("the same word twice is on the record as %+v; want it once", got)
	}
	word(true, "")
	if got := deliveredFor(rt, id); len(got) != 2 || !got[1].OK {
		t.Fatalf("the seat's newer word, that it arrived, is not on the record: %+v", got)
	}
}
