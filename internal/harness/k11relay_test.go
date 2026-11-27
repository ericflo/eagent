package harness

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// A turn of eagent's is typing a relay_send into the agent when the user
// quits. The typing ends (here it fails part way, so the send "may have
// gone") just after finish has begun. On main, relay_send typed in the
// tool and recorded the failure through r.sync, which finish's drain runs:
// the outcome is on the record before session.end and the turn ends at
// once. Here the send's end comes through postNews, which once finish has
// begun (loopDone) waits for finish's late loop; that loop is read only
// after the drain, and only while a seat taken out of use is pending. So
// the turn's relay_send waits for r.closed, the drain waits its whole grace
// for the turn ("a model call did not stop in time"), and with no seat
// retired the send's outcome is dropped: the record ends with the send and
// no word on how it went.
func TestSR8LoopAQuitWhileATurnTypesIntoTheAgentStallsAndDropsTheOutcome(t *testing.T) {
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	rt, _, done := r10Session(t, seat)
	// The tool runs as a turn's does: on a goroutine the drain waits for.
	rt.wg.Add(1)
	out := make(chan string, 1)
	go func() {
		defer rt.wg.Done()
		args := map[string]any{"relay": "codex", "text": "run the tests"}
		got, _ := rt.relayTool("relay_send", argStr(args), argNum(args), argList(args), false)
		out <- got
	}()
	var id string
	select {
	case id = <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("setup: the send was never typed")
	}
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("setup: finish never began")
	}
	seat.result <- &relay.TypingError{Err: errors.New("the pane went away mid-line")}
	stalled := false
	select {
	case <-rt.closed:
	case <-time.After(2 * time.Second):
		stalled = true
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	outcome := state.Replay(evs).SendOutcome(id)
	if stalled || outcome != state.SendUnconfirmed {
		t.Fatalf("a quit while a turn typed into the agent: the loop closed only after the drain's grace: %v; the send's outcome on the record: %q (main records the failed typing before session.end); tool said %q; log %q", stalled, outcome, recvOr(out), rt.ui.(*fakeUI).logged())
	}
}

// sr8SlowKillSeat is an agent whose terminal takes a moment to go: Kill
// waits for release (relay.Seat's Kill waits for its watcher, up to 30 s).
type sr8SlowKillSeat struct {
	*fakeSeat
	entered chan struct{}
	release chan struct{}
}

func (s *sr8SlowKillSeat) Kill() error {
	s.entered <- struct{}{}
	<-s.release
	return s.fakeSeat.Kill()
}

// The sibling: a turn's relay_control stop is killing the agent when the
// user quits. On main the stop killed, recorded relay.end through r.sync
// (finish's drain runs it) and returned: the turn ended at once. Here the
// stop then waits for the seat to drain, and the drain's news comes
// through postNews, which finish reads only after its own drain: the turn
// is held until the drain's grace runs out, the log says a model call did
// not stop, and the stop's result is never recorded.
func TestSR8LoopAQuitWhileATurnStopsTheAgentWaitsOutTheGrace(t *testing.T) {
	seat := &sr8SlowKillSeat{fakeSeat: newFakeSeat("one"), entered: make(chan struct{}, 1), release: make(chan struct{})}
	rt, _, done := r10Session(t, seat)
	rt.wg.Add(1)
	out := make(chan string, 1)
	go func() {
		defer rt.wg.Done()
		args := map[string]any{"relay": "codex", "action": "stop"}
		got, _ := rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false)
		out <- got
	}()
	select {
	case <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("setup: the stop never began its kill")
	}
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("setup: finish never began")
	}
	close(seat.release)
	stalled := false
	select {
	case <-rt.closed:
	case <-time.After(2 * time.Second):
		stalled = true
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	if stalled {
		t.Fatalf("a quit while a turn stopped the agent: the loop closed only after the drain's grace; tool said %q; log %q", recvOr(out), rt.ui.(*fakeUI).logged())
	}
}

func recvOr(ch <-chan string) string {
	select {
	case s := <-ch:
		return s
	default:
		return "(nothing yet)"
	}
}

// The switch's sibling: a turn's relay_control switch waits for its brief
// to be typed into the new agent (awaitSend(briefDone), then the drained
// barrier) when the user quits; the typing then fails part way. How it
// ended comes through the loop's one queue, which finish reads from its
// first step: the turn ends at once, the brief's outcome is on the record
// before session.end, and the drain does not wait out its grace.
func TestK11AQuitWhileATurnsSwitchTypesItsBrief(t *testing.T) {
	old := newFakeSeat("one")
	next := &heldSendSeat{fakeSeat: newFakeSeat("two"), entered: make(chan string, 4), result: make(chan error, 4)}
	rt, g, done := r10Session(t, old, next)
	rt.wg.Add(1)
	out := make(chan string, 1)
	go func() {
		defer rt.wg.Done()
		args := map[string]any{"action": "switch", "profile": "claude"}
		got, _ := rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false)
		out <- got
	}()
	g.waitEntered(t)
	g.release <- struct{}{}
	var id string
	select {
	case id = <-next.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("setup: the brief was never typed")
	}
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("setup: finish never began")
	}
	next.result <- &relay.TypingError{Err: errors.New("the pane went away mid-brief")}
	select {
	case <-rt.closed:
	case <-time.After(2 * time.Second):
		t.Errorf("the loop closed only after the drain's grace; tool said %q", recvOr(out))
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	if o := state.Replay(evs).SendOutcome(id); o != state.SendUnconfirmed {
		t.Errorf("the brief %s, typed in part as the user quit: %q on the record; want unconfirmed", id, o)
	}
}

// The relay reviewer's tests (sr8_relay_test.go), copied.

// sr8SlowSeat is a k2FeedSeat whose first Send is still under way when the
// seat is killed, as relay.Seat's is while it sleeps out interruptPause
// (1.5 s) or a dialogPause before it next looks at whether it was closed:
// it ends when the test lets it, with ErrClosed (nothing typed).
type sr8SlowSeat struct {
	*k2FeedSeat
	entered, release chan struct{}
	quick            int // sends typed at once before the slow one
}

func (s *sr8SlowSeat) Send(id, text, mode string) (string, error) {
	s.fakeSeat.mu.Lock()
	if s.quick > 0 {
		s.quick--
		s.fakeSeat.mu.Unlock()
		return s.k2FeedSeat.Send(id, text, mode)
	}
	s.sent = append(s.sent, text)
	s.fakeSeat.mu.Unlock()
	close(s.entered)
	<-s.release
	return "", relay.ErrClosed
}

// SR8-1. relay_control stop settles what the stopped agent was typed once
// its seat has drained and no send is being typed into it (retire); the
// settle is settleExited of whatever record holds the relay's name when it
// runs. The user's first message is still being typed into Codex when it
// is stopped; the seat drains, the stop returns, and the user restarts
// Codex (relay_control restart of the stopped relay) and sends a second
// message, typed into the new Codex. Then the first send's typing ends:
// the stop's settle runs now, against the new Codex's record, and reports
// the second message "could not be confirmed: the agent was restarted
// before its arrival showed" (a wake for eagent, may-not-have-arrived on
// the phone) while the new agent runs and nothing restarted it; its
// arrival then gives the send a second outcome.
func TestSR8AStopsLateSettleReportsTheNewAgentsSend(t *testing.T) {
	slow := &sr8SlowSeat{k2FeedSeat: newK2FeedSeat(t), entered: make(chan struct{}), release: make(chan struct{})}
	two := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{slow, two}, nil)
	rt := newLaunchRuntime(t)
	t.Cleanup(func() {
		select {
		case <-slow.release:
		default:
			close(slow.release)
		}
	})

	userSays(t, rt, "run the tests")
	<-slow.entered // being typed
	first := lastSendID(rt)

	stopped := relayToolAsync(rt, map[string]any{"action": "stop"})
	slow.waitGone(t)
	slow.end() // its watcher ends: drained, with the send still being typed
	recv(t, stopped, "relay_control stop")

	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, restarted, "relay_control restart")
	waitRelayStarts(t, rt)

	userSays(t, rt, "and then the lint")
	waitFor(t, "the second message typed into the new Codex", func() bool { return len(two.sentTexts()) == 1 })
	waitFor(t, "the new Codex's queue idle", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		return rt.relays.queues[two] == nil
	})
	second := lastSendID(rt)
	if second == first {
		t.Fatal("the second message was not sent")
	}
	if got := deliveredFor(rt, second); len(got) != 0 {
		t.Fatalf("%s already has an outcome before anything: %+v", second, got)
	}

	close(slow.release) // the first send's typing ends: nothing was typed
	waitFor(t, "the first send's outcome", func() bool { return len(deliveredFor(rt, first)) > 0 })
	rt.sync(func() {})
	if got := deliveredFor(rt, second); len(got) != 0 {
		t.Errorf("%s, typed into the new Codex, which runs: %+v; want no outcome yet (nothing stopped or restarted the agent it was typed into)", second, got)
	}
	two.say(relay.Event{Kind: relay.KindDelivered, SendID: second, OK: true})
	rt.sync(func() {})
	if got := deliveredFor(rt, second); len(got) != 1 || !got[0].OK {
		t.Errorf("%s after its arrival: %+v; want one outcome, arrived", second, got)
	}
}

// SR8-1, the known limit "a fresh start under the name leaves it
// unresolved", which this pass set out to close: Codex was typed the
// user's first message (not yet confirmed) and is typing the second when
// it is stopped. The seat drains, the stop returns, and a fresh Codex is
// started (relay_start, which takes the stopped relay's name). The second
// send's typing ends after that: the stop's settle runs then, on the new
// Codex's record, and the first message, typed into the stopped agent,
// never gets an outcome: it stays on its way on the record for good (an
// ended relay is not reattached).
func TestSR8AStopsLateSettleLeavesTheStoppedAgentsSendUnresolved(t *testing.T) {
	slow := &sr8SlowSeat{k2FeedSeat: newK2FeedSeat(t), entered: make(chan struct{}), release: make(chan struct{}), quick: 1}
	two := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{slow, two}, nil)
	rt := newLaunchRuntime(t)
	t.Cleanup(func() {
		select {
		case <-slow.release:
		default:
			close(slow.release)
		}
	})
	_, typed := k2Forward(t, rt, slow.k2FeedSeat, "run the tests", "queue")
	userSays(t, rt, "and then the lint")
	<-slow.entered

	stopped := relayToolAsync(rt, map[string]any{"action": "stop"})
	slow.waitGone(t)
	slow.end()
	recv(t, stopped, "relay_control stop")

	started := make(chan string, 1)
	go func() {
		args := map[string]any{"profile": "codex"}
		text, _ := rt.relayTool("relay_start", argStr(args), argNum(args), argList(args), false)
		started <- text
	}()
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, started, "relay_start")
	waitRelayStarts(t, rt)
	rt.sync(func() {
		if rel := rt.st.Relays["codex"]; rel == nil || rel.Ended {
			t.Errorf("the fresh start did not take the name codex: %+v", rel)
		}
	})

	close(slow.release)
	waitFor(t, "the stopped agent's queue done", func() bool {
		rt.relays.mu.Lock()
		defer rt.relays.mu.Unlock()
		return rt.relays.queues[slow] == nil
	})
	rt.sync(func() {})
	if got := deliveredFor(rt, typed); len(got) != 1 {
		t.Errorf("%s, typed into the agent that was stopped: outcomes %+v; want one (unconfirmed: the agent was stopped)", typed, got)
	}
}

// The relay reviewer's suspicion: a send's failed outcome and the agent's
// exit come in the same look while a change waits for its turn. The
// failed outcome looks at the waiting change (appendOutcome) while the
// record still says idle; the seat already says exited. The exit rule
// holds there too: an agent that exited is not started again; the change
// is what its next start runs.
func TestK11AFailedOutcomeInTheExitsLookDoesNotRestartTheExitedAgent(t *testing.T) {
	seat := newFakeSeat("one")
	useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	seat.emit(relay.Event{Kind: relay.KindState, State: "working"})
	k1Relay(t, rt, "the agent in a turn", func(rel *state.Relay) bool { return rel.State == "working" })
	if msg := k1Repick(t, rt); !strings.Contains(msg, "when the turn ends") {
		t.Fatalf("repick in a turn: %q", msg)
	}
	rt.sync(func() { // one look, said at once, before the loop takes any of it
		seat.emit(relay.Event{Kind: relay.KindState, State: "idle"},
			relay.Event{Kind: relay.KindDelivered, SendID: "r9", OK: false, Outcome: relay.OutcomeUnconfirmed, Detail: "never showed up"},
			relay.Event{Kind: relay.KindState, State: "exited", Detail: "$ "})
	})
	waitFor(t, "the failed outcome recorded", func() bool { return len(deliveredFor(rt, "r9")) > 0 })
	rt.sync(func() {}) // and the exit, after it
	if k1Restarting(rt) {
		t.Fatal("the agent that exited was started again by the waiting change")
	}
	rt.sync(func() {
		if _, ok, waiting := rt.nextPick("codex"); !ok || waiting {
			t.Errorf("after the exit: change kept %v, waiting %v; want it kept for the next start", ok, waiting)
		}
	})
}
