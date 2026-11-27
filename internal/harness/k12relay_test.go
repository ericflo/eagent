package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// The ninth review's suspicion: relay_send stops waiting for its send's
// typing because the session ends (awaitSend at r.closed), and the typing
// then succeeds. The tool said "Could not send to Codex: the session is
// ending", which a resumed model reads as not sent and may send again. It
// says what is true: the session ended before the send was confirmed, and
// the send's own outcome decides.
func TestK12ASendTheSessionEndedOnSaysSo(t *testing.T) {
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	out := make(chan string, 1)
	go func() {
		text, _ := rt.relayTool("relay_send", argStr(map[string]any{"text": "run the tests"}), argNum(nil), argList(nil), false)
		out <- text
	}()
	id := <-seat.entered
	close(rt.closed) // the session's end: the loop has stopped
	text := recv(t, out, "relay_send")
	if strings.Contains(text, "Could not send") || !strings.Contains(text, "ended before") {
		t.Errorf("relay_send as the session ended: %q; want that the session ended before the send was confirmed, not that it could not send", text)
	}
	seat.result <- nil // the typing goes through
	k1QueuesIdle(t, rt)
	for _, d := range deliveredFor(rt, id) {
		if d.Outcome == event.DeliveryNotSent {
			t.Errorf("%s, typed: recorded not sent (%q)", id, d.Detail)
		}
	}
}

// The same for a switch's brief: the switch stops waiting for the brief's
// typing as the session ends; it does not record the brief not sent, nor
// say it could not be typed: the brief's own outcome decides.
func TestK12ASwitchTheSessionEndedOnSaysSo(t *testing.T) {
	next := &heldSendSeat{fakeSeat: newFakeSeat("two"), entered: make(chan string, 4), result: make(chan error, 4)}
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), next}, nil)
	rt := newLaunchRuntime(t)
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	var id string
	select {
	case id = <-next.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the brief was never typed")
	}
	close(rt.closed)
	text := recv(t, switched, "switch")
	if strings.Contains(text, "could not be typed") || !strings.Contains(text, "ended before") {
		t.Errorf("switch as the session ended: %q; want that the session ended before the brief was confirmed", text)
	}
	next.result <- nil
	// Its typing ends (the barrier after it stays queued; once the loop
	// has closed nothing more is taken, and finish records what is left).
	waitFor(t, "the brief's typing ended", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		q := rt.relays.queues[next]
		return q == nil || q.typing == ""
	})
	for _, d := range deliveredFor(rt, id) {
		if d.Outcome == event.DeliveryNotSent {
			t.Errorf("the brief %s, typed: recorded not sent (%q)", id, d.Detail)
		}
	}
}

// The ninth review: the user quits while a turn's relay_send waits for its
// send to be typed (seat.Send waits for the agent's prompt, up to 40 s);
// the drain's grace runs out. Its log said "a model call did not stop in
// time"; it says what the turn waited on: the send being typed.
func TestK12ADrainSaysItWaitedOnASendBeingTyped(t *testing.T) {
	saved := turnStopGrace
	turnStopGrace = 20 * time.Millisecond
	t.Cleanup(func() { turnStopGrace = saved })
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	rt, _, done := r10Session(t, seat)
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		_, _ = rt.relayTool("relay_send", argStr(map[string]any{"text": "run the tests"}), argNum(nil), argList(nil), false)
	}()
	id := <-seat.entered
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.closed: // past the grace
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never closed")
	}
	seat.result <- nil // the typing ends (seat.Send is bounded: 40 s)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	logged := strings.Join(rt.ui.(*fakeUI).logged(), "\n")
	if strings.Contains(logged, "a model call did not stop in time") || !strings.Contains(logged, "being typed into Codex ("+id+")") {
		t.Errorf("the drain's log: %q; want that the turn waited on the send %s being typed into Codex", logged, id)
	}
}

// The relay reviewer's ninth-pass tests (sr9_relay_test.go), copied.

// sr9Outcomes lists every relay.delivered a finished session's file holds
// for a send.
func sr9Outcomes(t *testing.T, rt *Runtime, id string) []event.RelayDeliveredData {
	t.Helper()
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	var out []event.RelayDeliveredData
	for _, ev := range evs {
		var d event.RelayDeliveredData
		if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id {
			out = append(out, d)
		}
	}
	return out
}

func sr9Wait(t *testing.T, ch <-chan int, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

// SR9-1. A start's end now comes through the loop's one queue, which
// finish reads in its retiring drain: a restart that comes up while finish
// waits for a stopped seat to drain is recorded there, before that seat's
// settle. The user's message is typed into Codex (not yet confirmed);
// Codex is stopped, and while its seat drains Codex is restarted (the
// restart waits for the stop) and the user quits. The stop stops waiting
// at the close, the restart comes up and finish records it: its
// settleExited of the stopped record reports the message unconfirmed
// ("the agent was stopped before its arrival showed"). The stopped seat,
// still unsettled, then says the message arrived, and that is recorded
// too: two outcomes for one send.
func TestSR9RelayRestartAfterAStopRecordedByFinishGivesTheSendTwoOutcomes(t *testing.T) {
	one := newK2FeedSeat(t)
	two := newFakeSeat("two")
	rt, g, done := r10Session(t, one, two)
	doneSeen := make(chan int, 1)
	go func() { doneSeen <- <-done }()
	_, id := k2Forward(t, rt, one, "run the tests", "queue")

	stopped := relayToolAsync(rt, map[string]any{"action": "stop"})
	one.waitGone(t) // killed; its seat has not drained
	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	waitFor(t, "the restart waiting for the stop", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		job := rt.relays.launching["codex"]
		return job != nil && job.afterStop != nil
	})
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop never closed")
	}
	recv(t, stopped, "relay_control stop")
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, restarted, "relay_control restart")

	// The stopped agent's last read: the message had arrived.
	one.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
	one.end()
	sr9Wait(t, doneSeen, "the session's end")

	got := sr9Outcomes(t, rt, id)
	if len(got) != 1 {
		t.Errorf("%s has %d outcomes on the record: %+v; want one (a send has one outcome)", id, len(got), got)
	}
}

// SR9-1, the sibling of SR7-3 (TestSR7AQuitDuringARestartDropsTheOldSeatsArrival)
// with the old seat's last look after the new agent is up. A model change
// restarts Codex; the user quits while the old seat still drains. The
// restart stops waiting for that drain at the close (job.closed), and its
// end now comes through the queue finish reads while it waits for the old
// seat: finishLaunch settles the old seat there (settleRetired, then
// settleExited: unconfirmed). The old seat's last look, read arriving, is
// then logged and not recorded: the message that arrived is on the record
// as "cannot be confirmed". Before 4bfe893 the restart's end waited for
// finish's retiring drain (abandonLaunch, overtaken), so the arrival was
// recorded first.
func TestSR9RelayQuitDuringARestartRecordedBeforeTheOldSeatDrainsDropsItsArrival(t *testing.T) {
	seat := newK2FeedSeat(t)
	rt, g, done := r10Session(t, seat, newFakeSeat("two"))
	doneSeen := make(chan int, 1)
	go func() { doneSeen <- <-done }()
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	rt.sync(func() {
		if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("restart: %v", err)
		}
	})
	seat.waitGone(t)
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop never closed")
	}
	g.waitEntered(t)
	g.release <- struct{}{}
	waitFor(t, "the restart taken by finish", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		return len(rt.relays.launching) == 0
	})
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true}) // its last look
	seat.end()
	sr9Wait(t, doneSeen, "the session's end")
	got := sr9Outcomes(t, rt, id)
	if len(got) != 1 || !got[0].OK {
		t.Errorf("%s, read arriving by the old seat as the user quit: %+v; want its one outcome, arrived", id, got)
	}
}

// SR9-1, the same with Stop session (K9's case, TestK9StopSessionDuringARestartTakesTheOldSeatsArrival,
// run through finish): the restart, stopped, comes up while the old seat
// still drains; finish takes its end from the queue and settles the old
// seat there, before the old seat's last look, which is then not recorded.
func TestSR9RelayStopSessionDuringARestartRecordedBeforeTheOldSeatDrainsDropsItsArrival(t *testing.T) {
	seat := newK2FeedSeat(t)
	rt, g, done := r10Session(t, seat, newFakeSeat("two"))
	doneSeen := make(chan int, 1)
	go func() { doneSeen <- <-done }()
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	rt.sync(func() {
		if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("restart: %v", err)
		}
	})
	seat.waitGone(t)
	rt.sync(func() { rt.stopSession("the user stopped the session") })
	select {
	case <-rt.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop never closed")
	}
	g.waitEntered(t)
	g.release <- struct{}{}
	waitFor(t, "the restart taken by finish", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		return len(rt.relays.launching) == 0
	})
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true}) // its last look
	seat.end()
	sr9Wait(t, doneSeen, "the session's end")
	got := sr9Outcomes(t, rt, id)
	if len(got) != 1 || !got[0].OK {
		t.Errorf("%s, read arriving by the old seat at Stop session: %+v; want its one outcome, arrived", id, got)
	}
}

// The ninth review's suspicion: a model change restarts Codex as the user
// quits; the new Codex is recorded while the old seat still drains, and
// the old seat's last look says the agent it watched moved to another
// session. That move is the stopped agent's: it does not land on the new
// agent's record (a resume would open the wrong conversation).
func TestK12AStoppedAgentsLateMoveDoesNotLandOnTheNewRecord(t *testing.T) {
	seat := newK2FeedSeat(t)
	rt, g, done := r10Session(t, seat, newFakeSeat("two"))
	doneSeen := make(chan int, 1)
	go func() { doneSeen <- <-done }()
	rt.sync(func() {
		if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("restart: %v", err)
		}
	})
	seat.waitGone(t)
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop never closed")
	}
	g.waitEntered(t)
	g.release <- struct{}{}
	waitFor(t, "the restart taken by finish", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		return len(rt.relays.launching) == 0
	})
	seat.say(relay.Event{Kind: relay.KindInner, InnerID: "sess-stopped-agent"}) // its last look
	seat.end()
	sr9Wait(t, doneSeen, "the session's end")
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	started := false
	for _, ev := range evs {
		var d event.RelayInnerData
		switch {
		case ev.Type == event.RelayStart:
			started = true
		case started && ev.Type == event.RelayInner && ev.Decode(&d) == nil && d.InnerID == "sess-stopped-agent":
			t.Errorf("the stopped agent's move was recorded after the new agent's start, on its record")
		}
	}
}

// SR9-2, the known limit "a send typed into an agent that is then stopped
// stays unresolved ... A fresh start under the name leaves it unresolved",
// through a process that dies in the stop's wait. relay_control stop
// records relay.end at once and settles what Codex was typed only once its
// seat has drained (up to 30 s of kill and 15 s of drain). eagent is cut
// off in that wait (killed, the machine sleeps, a crash). The resume skips
// the ended relay (reattachRelays reads only active ones), and a fresh
// Codex started under the name settles nothing of the record it replaces:
// the message typed into the stopped Codex has no outcome for good.
func TestSR9RelayACutDuringAStopsDrainLeavesTheSendUnresolvedAfterAFreshStart(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	relayToolAsync(rt, map[string]any{"action": "stop"})
	seat.waitGone(t) // relay.end is on the record; the seat has not drained
	ended := false
	rt.sync(func() { ended = rt.st.Relays["codex"].Ended })
	if !ended {
		t.Fatal("setup: the stop did not record the end")
	}
	path, project := rt.sess.Path, rt.opts.Project
	rt.sess.Close() // the process is cut off here

	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt2.sess.Close() })
	relayServeLoop(t, rt2)
	started := make(chan string, 1)
	go func() {
		args := map[string]any{"profile": "codex"}
		text, _ := rt2.relayTool("relay_start", argStr(args), argNum(args), argList(args), false)
		started <- text
	}()
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, started, "relay_start")
	waitRelayStarts(t, rt2)
	var name, outcome string
	rt2.sync(func() {
		if rel := rt2.st.Relays["codex"]; rel != nil && !rel.Ended {
			name = rel.Name
		}
		outcome = string(rt2.st.SendOutcome(id))
	})
	if name != "codex" {
		t.Fatalf("setup: the fresh start did not take the name codex")
	}
	switch outcome {
	case "unconfirmed", "not_sent", "arrived":
	default:
		t.Errorf("%s, typed into the stopped Codex before the cut: outcome %q after the resume and a fresh start; want one (unconfirmed: the agent was stopped)", id, outcome)
	}
}
