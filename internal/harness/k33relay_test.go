package harness

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
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

// k33StuckSeat is a seat whose Send hangs until released, then ends as
// told: as a real seat's does once its terminal was closed under it
// (relay.ErrClosed), or typed.
type k33StuckSeat struct {
	*k2FeedSeat
	entered chan string
	release chan error
}

func (s *k33StuckSeat) Send(id, text, mode string) (_ string, err error) {
	s.entered <- text
	return "", <-s.release
}

// A message of the user's is being typed into Codex as a switch begins,
// and its typing outlasts the switch's wait for it (oldSendSettle). The
// brief, written without it in hand, quotes it whole as a message that
// waits. When the typing then ended with the terminal closed, the message
// was recorded as not sent ("the agent is not running"): the model was
// told it did not go, though the new agent had it whole in the brief, and
// forwarding it again gave it to the new agent twice. It is left to the
// brief, as the user's other messages to the old agent are, however its
// typing ends.
func TestK33AMessageStillBeingTypedAtASwitchIsLeftToTheBrief(t *testing.T) {
	saved := oldSendSettle
	oldSendSettle = 50 * time.Millisecond
	t.Cleanup(func() { oldSendSettle = saved })
	for _, c := range []struct {
		name string
		ends error
	}{
		{"its terminal closed under it", relay.ErrClosed},
		{"typed as the agent was stopped", nil},
		{"typed part way", &relay.TypingError{Err: fmt.Errorf("the paste failed")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			seat := &k33StuckSeat{k2FeedSeat: newK2FeedSeat(t), entered: make(chan string, 1), release: make(chan error, 1)}
			next := newFakeSeat("two")
			g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
			rt := newLaunchRuntime(t)
			rt.sync(func() {
				d := event.UserMessageData{Text: "fix the flaky test please", Delivery: "queue"}
				ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
				rel, why := rt.routeUserMessage(d)
				if rel == nil {
					t.Errorf("not routed: %s", why)
					return
				}
				rt.forwardUserMessage(rel, ev.Seq, d)
			})
			select {
			case <-seat.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("the message was never being typed")
			}
			id := lastSendID(rt)
			switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
			seat.waitGone(t)
			seat.end()
			g.release <- struct{}{}
			recv(t, switched, "switch")
			waitFor(t, "the brief typed", func() bool { return len(next.sentTexts()) > 0 })
			if brief := next.sentTexts()[0]; !strings.Contains(brief, "- fix the flaky test please\n") {
				t.Fatalf("the brief does not quote the message still being typed:\n%s", brief)
			}
			seat.release <- c.ends
			k1QueuesIdle(t, rt)
			if got := deliveredFor(rt, id); len(got) != 0 || k2Outcome(rt, id) != state.SendWaiting {
				t.Fatalf("the brief gave the new agent the message whole, and the record says of it %+v (%s); want it left to the brief, with no outcome", got, k2Outcome(rt, id))
			}
			if texts := next.sentTexts(); len(texts) != 1 {
				t.Fatalf("typed into the new agent: %d sends; want the brief alone", len(texts))
			}
		})
	}
}

// The same message, forwarded by the model itself: relay_forward waits on
// the typing, and when the typing ended it recorded the failure as the
// message's outcome on its own and told the model the message could not
// be sent, beside a brief that had given it to the new agent whole. The
// tool call says what happened and records nothing.
func TestK33AForwardStillBeingTypedAtASwitchIsLeftToTheBrief(t *testing.T) {
	saved := oldSendSettle
	oldSendSettle = 50 * time.Millisecond
	t.Cleanup(func() { oldSendSettle = saved })
	for _, c := range []struct {
		name string
		ends error
	}{
		{"its terminal closed under it", relay.ErrClosed},
		{"typed as the agent was stopped", nil},
		{"typed part way", &relay.TypingError{Err: fmt.Errorf("the paste failed")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			seat := &k33StuckSeat{k2FeedSeat: newK2FeedSeat(t), entered: make(chan string, 1), release: make(chan error, 1)}
			next := newFakeSeat("two")
			g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
			rt := newLaunchRuntime(t)
			var seq int64
			rt.sync(func() {
				seq = rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "fix the flaky test please"})).Seq
			})
			fwd := make(chan string, 1)
			go func() {
				text, isErr := forward(rt, map[string]any{"seq": seq})
				fwd <- fmt.Sprintf("%v %s", isErr, text)
			}()
			select {
			case <-seat.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("the message was never being typed")
			}
			id := lastSendID(rt)
			switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
			seat.waitGone(t)
			seat.end()
			g.release <- struct{}{}
			recv(t, switched, "switch")
			waitFor(t, "the brief typed", func() bool { return len(next.sentTexts()) > 0 })
			if brief := next.sentTexts()[0]; !strings.Contains(brief, "- fix the flaky test please\n") {
				t.Fatalf("the brief does not quote the message still being typed:\n%s", brief)
			}
			seat.release <- c.ends
			out := recv(t, fwd, "relay_forward")
			k1QueuesIdle(t, rt)
			if got := deliveredFor(rt, id); len(got) != 0 || k2Outcome(rt, id) != state.SendWaiting {
				t.Fatalf("the brief gave the new agent the message whole, and the record says of it %+v (%s); want it left to the brief, with no outcome", got, k2Outcome(rt, id))
			}
			if !strings.HasPrefix(out, "false ") || !strings.Contains(out, "quotes the message whole") || strings.Contains(out, "Could not send") {
				t.Fatalf("the model was told %q; want it told the brief carries the message, and not that it could not be sent", out)
			}
		})
	}
}

// A message sent to one agent and again to another that still runs: a
// switch away from the first does not take the second send for its brief's
// (it waits for the agent it was sent to), so when that send's typing fails
// it is recorded as any failed typing is.
func TestK33ASendToAnAgentOutsideTheSwitchIsNotTheBriefs(t *testing.T) {
	useGatedLaunches(t, []relaySeat{newFakeSeat("one")}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		seq := rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "fix the flaky test please"})).Seq
		rt.append(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "pi", Profile: "pi", Agent: "pi"}))
		rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: "r1", From: "user", Seq: seq, Mode: "queue", Text: "fix the flaky test please"}))
		rt.append(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: true}))
		rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "pi", ID: "r2", From: "user", Seq: seq, Mode: "queue", Text: "fix the flaky test please"}))
		brief, carried := rt.briefOf(rt.handoverFor(rt.st.Relays["codex"]), nil)
		if len(carried) != 0 {
			t.Errorf("the brief from codex carries %v; want nothing: r1 was read, and r2 waits for pi, which this switch does not replace", carried)
		}
		if !strings.Contains(brief, "- fix the flaky test please\n") || strings.Contains(brief, "not yet shown as read") {
			t.Errorf("the brief does not quote the message codex read as history:\n%s", brief)
		}
	})
}

// A message still being typed when a restart begins, whose typing outlasts
// the restart's wait and then ends with the terminal closed: nothing was
// typed, and the record and the tool call said "the agent is not running"
// of an agent that had been restarted and was up. They say what happened.
func TestK33AMessageCutOffByARestartIsNotSaidToHaveNoAgent(t *testing.T) {
	saved := oldSendSettle
	oldSendSettle = 50 * time.Millisecond
	t.Cleanup(func() { oldSendSettle = saved })
	seat := &k33StuckSeat{k2FeedSeat: newK2FeedSeat(t), entered: make(chan string, 1), release: make(chan error, 1)}
	next := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{seat, next}, nil)
	rt := newLaunchRuntime(t)
	var seq int64
	rt.sync(func() {
		seq = rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "fix the flaky test please"})).Seq
	})
	fwd := make(chan string, 1)
	go func() {
		text, isErr := forward(rt, map[string]any{"seq": seq})
		fwd <- fmt.Sprintf("%v %s", isErr, text)
	}()
	select {
	case <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the message was never being typed")
	}
	id := lastSendID(rt)
	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	seat.waitGone(t)
	seat.end()
	g.release <- struct{}{}
	recv(t, restarted, "restart")
	waitRelayStarts(t, rt)
	seat.release <- relay.ErrClosed
	out := recv(t, fwd, "relay_forward")
	rt.sync(func() {})
	got := deliveredFor(rt, id)
	if len(got) != 1 || got[0].Outcome != event.DeliveryNotSent || !strings.Contains(got[0].Detail, "stopped or restarted before the message was typed") {
		t.Fatalf("the record says of %s %+v; want it not sent, for the restart", id, got)
	}
	if !strings.HasPrefix(out, "true ") || strings.Contains(out, "not running") {
		t.Fatalf("the model was told %q; want the failure with its true reason", out)
	}
}
