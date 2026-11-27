package harness

// The sixteenth review of the relay area, third pass (sr3): starts,
// restarts, switches and stops, sends and their outcomes, waiting model
// changes, live settings.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// sr3CommandSeat is pi as its seat reports one of its own commands typed
// into its prompt: the first send waits for the test (it is being typed),
// then the seat says it was typed (delivered, as Seat.Send's emitAside does
// for a command), and, as pi runs /new and the seat's next look polls pi's
// status file, that pi moved to another session and is idle there.
type sr3CommandSeat struct {
	*fakeSeat
	entered chan string
	gate    chan struct{}
	once    sync.Once
	movedTo string
}

func (s *sr3CommandSeat) Send(id, text, mode string) (string, error) {
	first := false
	s.once.Do(func() { first = true })
	if !first {
		return s.fakeSeat.Send(id, text, mode)
	}
	s.entered <- text
	<-s.gate
	s.fakeSeat.mu.Lock()
	s.fakeSeat.sent = append(s.fakeSeat.sent, text)
	s.fakeSeat.mu.Unlock()
	s.emit(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true},
		relay.Event{Kind: relay.KindInner, InnerID: s.movedTo},
		relay.Event{Kind: relay.KindState, State: "idle"})
	return "typed the command into pi", nil
}

func sr3PiRuntime(t *testing.T) *Runtime {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "pi"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	relayServeLoop(t, rt)
	rt.sync(func() {
		rt.append(event.New(event.RelayInner, event.ActorHarness, event.RelayInnerData{Name: "pi", InnerID: "sess-old"}))
		rt.st.Relays["pi"].State = "idle"
	})
	return rt
}

// SR3-1. pi is idle on session sess-old. eagent types pi's own /new into it
// (relay_send) and, while that is being typed, the user picks another
// effort in the thread's settings. The change waits ("it starts again ...
// once that is typed", 197a5b8) and is applied the instant the command's
// arrival is recorded (appendOutcome): the restart's session id is read
// from the record then (prepareLaunch), before pi has run /new or the
// seat's next look has seen the move, and the old seat's later news is no
// longer the relay's. pi is started again with --session-id sess-old: the
// conversation /new left (the eighth review's fault, by a new path). The
// /new is reported delivered and its effect is undone.
func TestSR3AChangeWaitingOnATypedNewRestartsPiIntoTheSessionItLeft(t *testing.T) {
	t.Skip("known, not fixed: SR3-1 needs the seat to say when the agent has acted on a typed command")
	seat := &sr3CommandSeat{fakeSeat: newFakeSeat("…"), entered: make(chan string, 4), gate: make(chan struct{}), movedTo: "sess-new"}
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := sr3PiRuntime(t)
	sent := make(chan string, 1)
	go func() {
		out, _ := rt.relayTool("relay_send", argStr(map[string]any{"text": "/new"}), argNum(nil), argList(nil), false)
		sent <- out
	}()
	recv(t, seat.entered, "/new being typed")
	var msg string
	var err error
	rt.sync(func() { msg, err = rt.repickRelay("pi", relayPick{"", "high"}) })
	if err != nil {
		t.Fatalf("repick: %v", err)
	}
	t.Logf("change: %q", msg)
	close(seat.gate)
	recv(t, sent, "relay_send")
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	var inner string
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelayStartData
			if ev.Type == event.RelayStart && ev.Decode(&d) == nil {
				inner = d.InnerID
			}
		}
	})
	if inner != "sess-new" {
		t.Errorf("pi was started again with --session-id %q; /new had moved it to sess-new, so the restart reopens the conversation /new left", inner)
	}
}

// sr3HandSeat is Claude's seat handing a held message over: the send waits
// for the test, then leaves the message in the spool (as spoolMessage
// does, unclaimed: Claude is in its turn and no Stop hook has run) and
// returns "held", the seat not yet closed.
type sr3HandSeat struct {
	*fakeSeat
	entered, returned chan string
	gate              chan struct{}
	mu                sync.Mutex
	spool, file       string
}

func (s *sr3HandSeat) Send(id, text, mode string) (string, error) {
	s.entered <- text
	<-s.gate
	s.mu.Lock()
	s.file = filepath.Join(s.spool, fmt.Sprintf("%d-%s.txt", time.Now().UnixNano(), id))
	err := os.WriteFile(s.file, []byte(text), 0o644)
	s.mu.Unlock()
	s.returned <- id
	if err != nil {
		return "", err
	}
	return "held for Claude; it reads it when its current turn ends", nil
}

// SR3-2. The known limit "a held message still in the spool when its agent
// exits or is restarted ... is reported unconfirmed", which fb011ca set out
// to close for a stop: Stop session (the app's or the web UI's Stop) runs
// in the loop step in which the seat has just handed a user's after-turn
// message to Claude's spool and its settleSend waits for the loop.
// settleExited skips the send being typed (q.typing) and marks it gone,
// without the spool take-back it gives every other held send; settleSend
// then records it unconfirmed ("whether it arrived cannot be confirmed").
// The message never reached Claude, which was killed before any Stop hook
// took it, and it is left in the spool for no one: the phone keeps "sent"
// with no Retry, and relay_forward refuses it as possibly arrived.
func TestSR3AHandOverEndingAsStopSessionRunsIsTakenBackNotSent(t *testing.T) {
	seat := &sr3HandSeat{fakeSeat: newFakeSeat("…"), entered: make(chan string, 4), returned: make(chan string, 4), gate: make(chan struct{})}
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := k3ClaudeRuntime(t)
	spool := filepath.Join(rt.sess.Path, "relays", "claude", "after_turn")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	seat.mu.Lock()
	seat.spool = spool
	seat.mu.Unlock()
	rt.sync(func() {
		d := event.UserMessageData{Text: "then run the linter", Delivery: "after_turn"}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("not routed: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
	recv(t, seat.entered, "the hand-over")
	id := lastSendID(rt)
	rt.sync(func() {
		close(seat.gate)
		<-seat.returned // handed over; its settleSend waits for this step
		rt.stopSession("the user stopped the session")
	})
	waitFor(t, "an outcome for the held message", func() bool { return len(deliveredFor(rt, id)) > 0 })
	k1QueuesIdle(t, rt)
	seat.mu.Lock()
	file := seat.file
	seat.mu.Unlock()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the message is still unclaimed in the spool after Stop session (%v)", err)
	}
	if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
		t.Errorf("%s, unclaimed in the spool when Stop session killed Claude: %+v; want one outcome, not_sent", id, got)
	} else if strings.Contains(got[0].Detail, "cannot be confirmed") {
		t.Errorf("detail %q", got[0].Detail)
	}
}

// A check that held (not a finding): Ctrl-C cancels Run's context, and Run
// turns that into beginShutdown (r.ending), so a newer change the end
// overtakes is said as not made and the agent finish records is kept.
func TestSR3CtrlCDuringAChangeWithANewerOneWaitingKeepsTheAgent(t *testing.T) {
	one, two, three := newFakeSeat("one"), newFakeSeat("two"), newFakeSeat("three")
	g := useGatedLaunches(t, []relaySeat{one, two, three}, nil)
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex", Interactive: true}, &fakeUI{input: make(chan string, 16)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	rt.sync(func() {
		rt.st.Relays["codex"].State = "idle"
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	rt.sync(func() {
		msg, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", "max"})
		t.Logf("newer change: %q %v", msg, err)
	})
	cancel() // Ctrl-C
	select {
	case <-rt.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never closed")
	}
	g.release <- struct{}{}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	select {
	case <-g.entered: // a third start, begun after the session's end
		g.release <- struct{}{}
		waitFor(t, "the late third agent stopped", three.wasKilled)
	case <-time.After(2 * time.Second):
	}
	if two.wasKilled() {
		t.Error("Ctrl-C killed the Codex that finish recorded as started (the newer change was applied after the loop had ended)")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	said := false
	for _, ev := range evs {
		var d event.ErrorData
		if ev.Type == event.Error && ev.Decode(&d) == nil && strings.Contains(d.Text, "was not made") {
			said = true
		}
	}
	if !said {
		t.Error("the record does not say the newer change was not made")
	}
}

// SR3-3. The sibling of 45bf6d4 (a start the session's end overtakes is
// handed to finish) for a start that does not come up. A user's message is
// held for the end of Claude's turn, unclaimed in the spool, and Claude is
// started again (relay_control restart, or a model change); the user quits
// while it starts, and the launch fails. The restart's run took the message
// back from the spool (relayLaunch.takenBack) before launching, but only
// finishLaunch records a take-back as not sent, and abandonLaunch, for a
// start that failed (or came up after relayOpsGrace), records nothing of
// it. The session ends with the message on the record as still on its way
// and gone from the spool; the next run finds Claude not running and
// settleExited reports it unconfirmed ("whether it arrived cannot be
// confirmed"), so relay_forward refuses it, though it never left eagent.
func TestSR3AMessageTakenBackByARestartTheEndOvertakesIsNotSent(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, nil}, []error{nil, errors.New("tmux: the server exited")})
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "claude", Interactive: true}, &fakeUI{input: make(chan string, 16)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	rt.sync(func() {
		rt.st.Relays["claude"].State = "idle"
		d := event.UserMessageData{Text: "then run the linter", Delivery: "after_turn"}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("not routed: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
	waitFor(t, "the message handed over", func() bool { return len(seat.sentTexts()) == 1 })
	k1QueuesIdle(t, rt)
	id := lastSendID(rt)
	spool := filepath.Join(rt.sess.Path, "relays", "claude", "after_turn")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(spool, fmt.Sprintf("%d-%s.txt", time.Now().UnixNano(), id))
	if err := os.WriteFile(file, []byte("then run the linter"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt.sync(func() {
		if err := rt.restartRelayWith(rt.st.Relays["claude"], relayPick{"opus", ""}); err != nil {
			t.Errorf("restart: %v", err)
		}
	})
	seat.waitGone(t)
	seat.end()
	g.waitEntered(t) // the run has taken the message back and launches
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the restart did not take the message back (%v)", err)
	}
	if k6Late {
		// The launch is still under way when finish stops waiting for
		// starts: what it took back is recorded before session.end all
		// the same, and the agent that comes up after is not recorded.
		evs := r10Quit(t, rt, g, 0, done)
		r10NotSentBeforeEnd(t, evs, id)
		g.release <- struct{}{}
		return
	}
	evs := r10Quit(t, rt, g, 1, done)
	r10NotSentBeforeEnd(t, evs, id)
}

// k6Late runs SR3-3 with the restart's launch still under way when finish
// stops waiting for starts (relayOpsGrace).
var k6Late bool

func TestK6AMessageTakenBackByARestartStillLaunchingAtTheEndIsNotSent(t *testing.T) {
	saved := relayOpsGrace
	relayOpsGrace, k6Late = 10*time.Millisecond, true
	t.Cleanup(func() { relayOpsGrace, k6Late = saved, false })
	TestSR3AMessageTakenBackByARestartTheEndOvertakesIsNotSent(t)
}
