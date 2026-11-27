package harness

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// pastingSeat is a fake agent whose sends block once they begin, as a paste
// into a terminal can, until the seat is killed; the send then fails with
// fail (a TypingError by default: the text may already be in the terminal).
type pastingSeat struct {
	*fakeSeat
	entered  chan string
	gone     chan struct{}
	goneOnce sync.Once
	fail     error
}

func newPastingSeat() *pastingSeat {
	return &pastingSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), gone: make(chan struct{}),
		fail: &relay.TypingError{Err: errors.New("send-keys Enter: server exited")}}
}

func (p *pastingSeat) Send(id, text, mode string) (_ string, err error) {
	defer p.sendStart(id)(&err)
	p.entered <- id
	<-p.gone
	return "", p.fail
}

func (p *pastingSeat) Kill() error {
	p.goneOnce.Do(func() { close(p.gone) })
	return p.fakeSeat.Kill()
}

func (p *pastingSeat) waitTyping(t *testing.T) string {
	t.Helper()
	select {
	case id := <-p.entered:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("the send was never typed")
		return ""
	}
}

// deliveredFor lists every relay.delivered on the record for a send.
func deliveredFor(rt *Runtime, id string) []event.RelayDeliveredData {
	var out []event.RelayDeliveredData
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelayDeliveredData
			if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id {
				out = append(out, d)
			}
		}
	})
	return out
}

// Finding 9. A send half typed into an agent when a restart stops it may
// already be in that agent's terminal (a TypingError says so). It must not
// be handed to the new agent and typed a second time: it is reported once,
// unconfirmed.
func TestATypingErrorAtARestartIsNotTypedIntoTheNewAgent(t *testing.T) {
	old, next := newPastingSeat(), newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, "drop the temp table and rebuild")
	id := old.waitTyping(t)
	rt.sync(func() {
		// The restart a model change makes (a message on its way now holds
		// a change asked back, item 3; this test is about the restart).
		if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	waitFor(t, "the send's outcome", func() bool {
		return len(deliveredFor(rt, id)) > 0 || len(next.sentTexts()) > 0
	})
	if got := next.sentTexts(); slices.Contains(got, "drop the temp table and rebuild") {
		t.Fatalf("the new agent was typed %q: the message may already be in the old one", got)
	}
	got := deliveredFor(rt, id)
	if len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed {
		t.Fatalf("outcomes %+v, want one unconfirmed", got)
	}
}

// Finding 11, first part. A message whose first send to the agent that
// held the name certainly did not go, and whose retry arrived there before
// that agent was stopped, went to the earlier holder: the refusal says so,
// not that it went to the agent running now.
func TestARefusalNamesTheStoppedHolderWhenAnEarlierAttemptFailed(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	rec.failed("codex", "r1", event.DeliveryNotSent)
	rec.send("codex", "r2", seq)
	rec.add(event.RelayDelivered, event.RelayDeliveredData{Name: "codex", ID: "r2", OK: true})
	rec.add(event.RelayEnd, event.RelayEndData{Name: "codex", Reason: "stopped"})
	rec.start("codex", "codex")
	const want = "that message went as written (r2) to the agent that held the name codex before, which was stopped; Codex, running now, was not sent it as its own message (its hand-over brief may quote it); to give it to this one, send the words with relay_send"
	if id, why := forwardedAs(state.Replay(rec.evs), "codex", seq, ""); id != "r2" || why != want {
		t.Fatalf("refused as %q:\n %q\nwant r2:\n %q", id, why, want)
	}
}

// Finding 11, second part. A send typed and never confirmed when the relay
// is stopped is reported unconfirmed by the stop (known limit A5: it was
// left waiting until a restart). When that relay is restarted on its own
// conversation, the new process waits on nothing, and the send keeps its
// one outcome.
func TestASendLeftByAStopIsSettledOnceWhenTheRelayIsRestarted(t *testing.T) {
	old, next := newFakeSeat("one"), newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	var typed string
	rt.sync(func() {
		typed = rt.st.NextRelaySendID()
		rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: typed, From: "user", Mode: "queue", Text: "tidy the shelves"}))
	})
	control := func(action string) (string, bool) {
		args := map[string]any{"action": action}
		return rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false)
	}
	if text, isErr := control("stop"); isErr {
		t.Fatalf("stop: %s", text)
	}
	if got := deliveredFor(rt, typed); len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed {
		t.Fatalf("after the stop: %+v, want one unconfirmed", got)
	}
	restarted := make(chan string, 1)
	go func() {
		text, isErr := control("restart")
		if isErr {
			text = "error: " + text
		}
		restarted <- text
	}()
	g.waitEntered(t)
	g.release <- struct{}{}
	select {
	case text := <-restarted:
		if strings.HasPrefix(text, "error: ") {
			t.Fatalf("restart: %s", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the restart never finished")
	}
	var outcome state.SendOutcome
	rt.sync(func() { outcome = rt.st.SendOutcome(typed) })
	got := deliveredFor(rt, typed)
	if len(got) != 1 || got[0].Outcome != event.DeliveryUnconfirmed || outcome != state.SendUnconfirmed {
		t.Fatalf("after the restart: outcomes %+v, outcome %q; want one unconfirmed", got, outcome)
	}
	if s := next.sentTexts(); len(s) != 0 {
		t.Fatalf("typed into the restarted agent: %q", s)
	}
}

// gatedKillSeat is a fake agent whose Kill waits for gate, as killing a
// terminal can take seconds; dead is closed once it has.
type gatedKillSeat struct {
	*fakeSeat
	entered chan struct{}
	gate    chan struct{}
	dead    chan struct{}
	once    sync.Once
}

func (s *gatedKillSeat) Kill() error {
	s.once.Do(func() {
		close(s.entered)
		<-s.gate
		_ = s.fakeSeat.Kill()
		close(s.dead)
	})
	return nil
}

// Finding 14. An agent that came up for a restart the relay was stopped
// during is killed in the background. A restart asked for right after
// must wait for that kill, as it waits for a stop's (relays.stopping), and
// not launch into the same terminal while the kill runs.
func TestARestartWaitsForTheKillOfAnAgentStoppedWhileItStarted(t *testing.T) {
	old := newFakeSeat("one")
	mid := &gatedKillSeat{fakeSeat: newFakeSeat("mid"), entered: make(chan struct{}), gate: make(chan struct{}), dead: make(chan struct{})}
	next := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, mid, next}, nil)
	gated := launchRelay
	killedFirst := make(chan bool, 4)
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		select {
		case <-mid.dead:
			killedFirst <- true
		default:
			killedFirst <- false
		}
		return gated(spec, innerID, resume)
	}
	rt := newLaunchRuntime(t)
	var gateOnce sync.Once
	release := func() { gateOnce.Do(func() { close(mid.gate) }) }
	t.Cleanup(release)
	<-killedFirst // the session's own start
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	<-killedFirst
	control := func(action string) (string, bool) {
		args := map[string]any{"action": action}
		return rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false)
	}
	if text, isErr := control("stop"); isErr {
		t.Fatalf("stop: %s", text)
	}
	g.release <- struct{}{}
	select {
	case <-mid.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent that came up for the stopped restart was never killed")
	}
	waitRelayStarts(t, rt)
	restarted := make(chan string, 1)
	go func() {
		text, isErr := control("restart")
		if isErr {
			text = "error: " + text
		}
		restarted <- text
	}()
	waitFor(t, "the restart under way", func() bool {
		var job *relayLaunch
		rt.sync(func() { job = rt.relays.launch("codex") })
		return job != nil
	})
	var waits bool
	rt.sync(func() {
		rt.relays.mu.Lock()
		waits = rt.relays.launching["codex"].afterStop != nil
		rt.relays.mu.Unlock()
	})
	if !waits {
		t.Fatal("the restart does not wait for the kill still running under the name")
	}
	release()
	g.release <- struct{}{}
	select {
	case text := <-restarted:
		if strings.HasPrefix(text, "error: ") {
			t.Fatalf("restart: %s", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the restart never finished")
	}
	if !<-killedFirst {
		t.Fatal("the restart launched while the kill of the agent before it still ran")
	}
}

// heldSendSeat is a fake agent whose sends block once they begin until the
// test gives each its result.
type heldSendSeat struct {
	*fakeSeat
	entered chan string
	result  chan error
}

func (s *heldSendSeat) Send(id, text, mode string) (_ string, err error) {
	defer s.sendStart(id)(&err)
	s.entered <- id
	if err := <-s.result; err != nil {
		return "", err
	}
	return "Typed", nil
}

// typingQueue is the one seat queue running (typing id), read under
// relays.mu.
func typingQueue(t *testing.T, rt *Runtime, id string) *sendQueue {
	t.Helper()
	rt.relays.mu.Lock()
	defer rt.relays.mu.Unlock()
	for _, q := range rt.relays.queues {
		if q.running {
			return q
		}
	}
	t.Fatalf("no queue is typing %s", id)
	return nil
}

func waitIdle(t *testing.T, q *sendQueue) {
	t.Helper()
	select {
	case <-q.idle:
	case <-time.After(5 * time.Second):
		t.Fatal("the send queue never finished")
	}
}

func postExit(rt *Runtime) {
	rt.post(func() {
		rt.onRelayEvent("codex", relay.Event{Kind: relay.KindState, State: "exited", Detail: "pane gone"})
	})
}

// Finding 8, the double report. The agent exits just as typing a send
// fails: the exit is on the loop's queue before the failure's outcome, and
// runs after the queue stopped naming the send as typing. The send gets
// one outcome, not the exit's unconfirmed and then the failure's not_sent.
func TestASendFailingAsItsAgentExitsHasOneOutcome(t *testing.T) {
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, "check the version")
	var id string
	select {
	case id = <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the send was never typed")
	}
	q := typingQueue(t, rt, id)
	hold, holding := make(chan struct{}), make(chan struct{})
	rt.post(func() { close(holding); <-hold })
	<-holding
	postExit(rt)
	seat.result <- relay.ErrClosed
	close(hold)
	waitIdle(t, q) // its outcome reached the loop behind the exit
	got := deliveredFor(rt, id)
	if len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
		t.Fatalf("the send has %d outcomes: %+v; want one, not_sent (nothing was typed)", len(got), got)
	}
}

// The agent exits while a send still waits in the seat for it to be ready,
// with no keystroke made: the send is certainly not sent, so it is on the
// record as that, and the user's message can be forwarded again.
func TestASendNotYetTypedWhenItsAgentExitsIsNotSent(t *testing.T) {
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	var seq int64
	rt.sync(func() {
		d := event.UserMessageData{Text: "check the version"}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		seq = ev.Seq
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("not routed: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
	var id string
	select {
	case id = <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the send was never typed")
	}
	q := typingQueue(t, rt, id)
	postExit(rt)
	rt.sync(func() {})
	seat.result <- errors.New("the agent has exited")
	waitIdle(t, q)
	var outcome state.SendOutcome
	var refusal string
	rt.sync(func() {
		outcome = rt.st.SendOutcome(id)
		_, refusal = forwardedAs(rt.st, "codex", seq, "")
	})
	if got := deliveredFor(rt, id); len(got) != 1 || outcome != state.SendNotSent || refusal != "" {
		t.Fatalf("outcome %q (%+v), forward refusal %q; want one not_sent and a forward allowed", outcome, got, refusal)
	}
}

// Finding 8, the loss. The agent exits while a send is being typed, and
// the typing then succeeds: the process it went into is gone and nothing
// will confirm it. It is reported unconfirmed, not left waiting for ever.
func TestASendTypedAsItsAgentExitsIsNotLeftWaiting(t *testing.T) {
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, "check the version")
	var id string
	select {
	case id = <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the send was never typed")
	}
	q := typingQueue(t, rt, id)
	postExit(rt)
	rt.sync(func() {})
	seat.result <- nil
	waitIdle(t, q)
	var outcome state.SendOutcome
	rt.sync(func() { outcome = rt.st.SendOutcome(id) })
	got := deliveredFor(rt, id)
	if len(got) != 1 || outcome != state.SendUnconfirmed {
		t.Fatalf("outcome %q, %d on the record (%+v); want one unconfirmed", outcome, len(got), got)
	}
}
