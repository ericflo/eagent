package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// SR10-1. Codex is stopped; while its seat drains, Codex is restarted (the
// restart waits for the stop) and a message is sent to it, held for the
// restart. The user quits: the stop stops waiting at the close, the
// restart comes up and finish records it, and the held message is queued
// for the new agent (never typed: the loop has closed). The stop's settle,
// left on the stopped seat, runs at its drain, after the new record: it
// reads the stopped record's sends, which still list the held message, and
// skips the new seat's queue (settleExited's `current`), so the message
// that was never typed is recorded "the agent was stopped before its
// arrival showed, so whether it arrived cannot be confirmed". It did not
// go: not_sent (finish's own record of a send left in a queue).
func TestSR10RelayHeldSendForARestartAfterAStopIsNotReportedUnconfirmed(t *testing.T) {
	one := newK2FeedSeat(t)
	two := newFakeSeat("two")
	rt, g, done := r10Session(t, one, two)
	doneSeen := make(chan int, 1)
	go func() { doneSeen <- <-done }()

	stopped := relayToolAsync(rt, map[string]any{"action": "stop"})
	one.waitGone(t)
	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	waitFor(t, "the restart waiting for the stop", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		job := rt.relays.launching["codex"]
		return job != nil && job.afterStop != nil
	})
	args := map[string]any{"relay": "codex", "text": "also check the docs"}
	text, _ := rt.relayTool("relay_send", argStr(args), argNum(args), argList(args), false)
	if !strings.Contains(text, "typed in once it is up") {
		t.Fatalf("setup: relay_send not held for the restart: %q", text)
	}
	id := lastSendID(rt)

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
	one.end() // the stopped seat drains: the stop's settle runs
	sr9Wait(t, doneSeen, "the session's end")

	got := sr9Outcomes(t, rt, id)
	if len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
		t.Errorf("%s, held for the restart and never typed: outcomes %+v; want one, not_sent", id, got)
	}
}

// SR10-2. A restart (relay_control restart) is under way as the user
// quits; the old seat is killed but its watcher has not ended (a terminal
// watcher's close is bounded at 30 s, past finish's 15 s). finish waits its
// bound for the old seat, then ends the queue; the restart's end comes
// after (overtaken, recorded by finish). settleOld finds the old seat
// neither drained nor settled and the start's wait not bounded (it ended at
// the close, job.undrained false), leaves the settle on the entry, and
// nothing runs it again: the message typed into the old Codex ends the
// session with no outcome. bb95cba settled it here (settleRetired, then
// settleExited: unconfirmed), as the decided rule "past a bound a seat is
// settled as it stands" says.
func TestSR10RelayRestartRecordedAfterFinishsBoundLeavesTheOldSendUnsettled(t *testing.T) {
	saved := oldSendSettle
	oldSendSettle = 200 * time.Millisecond
	t.Cleanup(func() { oldSendSettle = saved })
	one := newK2FeedSeat(t)
	rt, g, done := r10Session(t, one, newFakeSeat("two"))
	doneSeen := make(chan int, 1)
	go func() { doneSeen <- <-done }()
	_, id := k2Forward(t, rt, one, "run the tests", "queue")

	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	one.waitGone(t) // killed; its watcher has not ended
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop never closed")
	}
	g.waitEntered(t)
	waitFor(t, "finish's bound for the old seat", func() bool {
		return strings.Contains(strings.Join(rt.ui.(*fakeUI).logged(), "\n"), "a stopped agent had not said all it saw")
	})
	g.release <- struct{}{}
	recv(t, restarted, "relay_control restart")
	sr9Wait(t, doneSeen, "the session's end")

	got := sr9Outcomes(t, rt, id)
	if len(got) != 1 {
		t.Errorf("%s, typed into the Codex the restart stopped: outcomes %+v; want one (unconfirmed: the agent was restarted)", id, got)
	}
}

// SR10-3. A switch hands Codex's work to Claude while the user's message
// typed into Codex is not yet confirmed: the switch settles only eagent's
// own words to Codex (finishLaunch: the brief quotes the user's), so the
// user's message stays waiting on the ended Codex record. On a resume, the
// new reattachRelays settles every ended relay with sends waiting, and
// records that message "could not be confirmed", which wakes eagent with
// "send it again", though Claude's brief carried it.
func TestSR10RelayResumeAfterASwitchCallsTheBriefedUserMessageUnconfirmed(t *testing.T) {
	one := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{one, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, one, "run the tests", "queue")
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	one.waitGone(t)
	one.end()
	g.waitEntered(t)
	g.release <- struct{}{}
	text := recv(t, switched, "switch")
	if !strings.Contains(text, "Handed over") {
		t.Fatalf("setup: switch: %q", text)
	}
	k1QueuesIdle(t, rt)
	before := k2Outcome(rt, id)
	var brief bool
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelaySendData
			if ev.Type == event.RelaySend && ev.Decode(&d) == nil && d.Name != "codex" && strings.Contains(d.Text, "run the tests") {
				brief = true
			}
		}
	})
	if !brief || before != "waiting" {
		t.Fatalf("setup: brief quotes the message %v, outcome before the resume %q", brief, before)
	}
	path, project := rt.sess.Path, rt.opts.Project
	rt.sess.Close()

	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt2.sess.Close() })
	if o := rt2.st.SendOutcome(id); o != before {
		t.Errorf("%s, the user's message the switch's brief carried: after a resume its outcome is %q (eagent is told to send it again); want it left as the switch left it", id, o)
	}
}

// SR10-4. Codex is stopped (its stop has settled); Codex is restarted and,
// while the restart is under way, a message is sent to it and held for the
// restart. eagent is cut off before the restart is recorded. The message
// was never typed into anything; the resume's new settle of ended relays
// records it "the agent was stopped before its arrival showed, so whether
// it arrived cannot be confirmed". It did not go: not_sent.
func TestSR10RelayResumeCallsASendHeldForACutRestartUnconfirmed(t *testing.T) {
	one := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{one, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	stopped := relayToolAsync(rt, map[string]any{"action": "stop"})
	one.waitGone(t)
	one.end()
	recv(t, stopped, "relay_control stop")
	relayToolAsync(rt, map[string]any{"action": "restart"})
	g.waitEntered(t) // the restart is launching
	args := map[string]any{"relay": "codex", "text": "also check the docs"}
	text, _ := rt.relayTool("relay_send", argStr(args), argNum(args), argList(args), false)
	if !strings.Contains(text, "typed in once it is up") {
		t.Fatalf("setup: relay_send not held for the restart: %q", text)
	}
	id := lastSendID(rt)
	path, project := rt.sess.Path, rt.opts.Project
	rt.sess.Close() // cut off here

	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt2.sess.Close() })
	if o := rt2.st.SendOutcome(id); o != "not_sent" {
		t.Errorf("%s, held for a restart that never came up, never typed: after the resume %q; want not_sent", id, o)
	}
	g.release <- struct{}{}
}
