package harness

import (
	"errors"
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
