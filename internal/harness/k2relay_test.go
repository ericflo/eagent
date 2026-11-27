package harness

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/runtimecontrol"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// k2FeedSeat is an agent whose seat's news the test hands over one event
// at a time (its events are unbuffered) and ends by closing them, as a
// real seat's watcher does once the seat is killed. It shows no arrival of
// what it is sent.
type k2FeedSeat struct {
	*fakeSeat
	feed       chan relay.Event
	gone       chan struct{}
	once, done sync.Once
}

func newK2FeedSeat(t *testing.T) *k2FeedSeat {
	s := &k2FeedSeat{fakeSeat: newFakeSeat("one"), feed: make(chan relay.Event), gone: make(chan struct{})}
	s.quietTo = func(ev relay.Event) {
		// Nothing more is heard once its events have ended (end) or it is
		// stopped (Kill).
		defer func() { _ = recover() }()
		select {
		case s.feed <- ev:
		case <-s.gone:
		}
	}
	t.Cleanup(s.end)
	return s
}

func (s *k2FeedSeat) Events() <-chan relay.Event { return s.feed }

func (s *k2FeedSeat) Send(id, text, mode string) (_ string, err error) {
	defer s.sendStart(id)(&err)
	s.mu.Lock()
	s.sent = append(s.sent, text)
	s.modes = append(s.modes, mode)
	s.mu.Unlock()
	if mode == "after_turn" {
		return "held for Codex to read when its turn ends", nil
	}
	return "typed into Codex", nil
}

func (s *k2FeedSeat) Kill() error {
	s.once.Do(func() { close(s.gone) })
	return nil
}

func (s *k2FeedSeat) Close() {}

// say hands the seat's watcher one event after another, then, as a real
// seat at the end of a look, that it is quiet if it has become so.
func (s *k2FeedSeat) say(evs ...relay.Event) {
	for _, ev := range evs {
		s.mu.Lock()
		s.noteLocked(ev)
		s.mu.Unlock()
		s.feed <- ev
	}
	s.mu.Lock()
	due := s.quietDueLocked()
	s.mu.Unlock()
	if due {
		s.feed <- relay.Event{Kind: relay.KindQuiet}
	}
}

// end closes the seat's events: it has nothing more to say.
func (s *k2FeedSeat) end() { s.done.Do(func() { close(s.feed) }) }

func (s *k2FeedSeat) waitGone(t *testing.T) {
	t.Helper()
	select {
	case <-s.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent was never stopped")
	}
}

// k2Until polls cond for at most d and says whether it held.
func k2Until(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// k2Forward has the user's message forwarded with delivery mode and
// returns its seq and send id once the seat's queue has typed it.
func k2Forward(t *testing.T, rt *Runtime, seat *k2FeedSeat, text, mode string) (int64, string) {
	t.Helper()
	var seq int64
	rt.sync(func() {
		d := event.UserMessageData{Text: text, Delivery: mode}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		seq = ev.Seq
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("not routed: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
	waitFor(t, "the message typed", func() bool { return len(seat.sentTexts()) == 1 })
	k1QueuesIdle(t, rt)
	return seq, lastSendID(rt)
}

func k2Outcome(rt *Runtime, id string) (o state.SendOutcome) {
	rt.sync(func() { o = rt.st.SendOutcome(id) })
	return o
}

// Review 15, finding 5. A message held for the end of Codex's turn is
// swept from the spool unread (not sent) as a model change restarts Codex,
// or a switch replaces it: the seat says so before its events close. That
// outcome is the send's (not "unconfirmed", nor held for good), and the
// user's message may be forwarded again.
func TestK2ANotSentFromTheSeatARestartReplacesIsKept(t *testing.T) {
	for _, how := range []string{"restart", "switch"} {
		seat := newK2FeedSeat(t)
		g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
		rt := newLaunchRuntime(t)
		seq, id := k2Forward(t, rt, seat, "then run the linter", "after_turn")
		var switched <-chan string
		if how == "restart" {
			// The restart a model change makes (a message on its way now
			// holds a change asked back, item 3).
			rt.sync(func() {
				if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
					t.Errorf("restart: %v", err)
				}
			})
		} else {
			switched = relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
		}
		seat.waitGone(t)
		// The second event is taken only once the first is posted; the sync
		// then runs after it: the outcome reached the loop before the new
		// agent is up.
		seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Outcome: relay.OutcomeNotSent, Detail: "it was still in the spool when the turn ended; taken back unread"},
			relay.Event{Kind: relay.KindActivity, Text: "closing"})
		rt.sync(func() {})
		seat.end()
		g.release <- struct{}{}
		if switched != nil {
			recv(t, switched, "switch")
		}
		waitRelayStarts(t, rt)
		if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
			t.Fatalf("%s: %s: %+v; want its one outcome, not_sent", how, id, got)
		}
		var prev, why string
		rt.sync(func() { prev, why = forwardedAs(rt.st, "codex", seq, "") })
		if prev != "" {
			t.Errorf("%s: forwarding the message again is refused as %s: %q", how, prev, why)
		}
	}
}

// Review 15, finding 6. Codex's transcript showed a message arriving just
// as a model change restarted it; the seat had that arrival in hand when
// it was killed, and its watcher hands it over only after the new agent is
// up. The restart waits for the old seat's news: the message arrived, and
// it is not also reported unconfirmed.
func TestK2AnArrivalTheReplacedSeatStillHadIsKept(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, seat, "run the tests", "queue")
	g.release <- struct{}{} // the new agent comes up at once
	unblock := make(chan struct{})
	rt.post(func() {
		// The restart a model change makes (a message on its way now holds
		// a change asked back, item 3).
		if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
		<-unblock // the loop is busy: what reaches it now waits, in order
	})
	seat.waitGone(t)
	// Whatever the restart has the loop do next is queued before the old
	// seat's arrival (a restart that does not wait for it).
	k2Until(100*time.Millisecond, func() bool { return len(rt.loop) > 0 })
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
	seat.end()
	close(unblock)
	waitRelayStarts(t, rt)
	if got := deliveredFor(rt, id); len(got) != 1 || !got[0].OK || k2Outcome(rt, id) != state.SendArrived {
		t.Fatalf("%s: %+v; want its one outcome, arrived", id, got)
	}
}

// k2LiveModel applies a live-settings change of the relay's model as the
// controller does, on the loop, and returns applyRuntimeSettings' answer.
func k2LiveModel(t *testing.T, rt *Runtime, name, model string) error {
	t.Helper()
	var err error
	rt.sync(func() {
		c := rt.runtimeControl
		view, verr := runtimecontrol.View(rt.opts.Project, rt.sess.ID, c.Generation, c.Values, true)
		if verr != nil {
			err = verr
			return
		}
		grant, _ := runtimecontrol.Grant(rt.opts.Project, rt.sess.ID)
		proposal := control.Proposal{Operation: "settings.apply", SchemaVersion: runtimecontrol.Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation,
			Edits: []control.Edit{{Op: "set", Key: "/relays/" + name + "/model", Value: model}}}
		values, verr := runtimecontrol.ApplyValues(rt.opts.Project, c.Values, proposal, view, grant)
		if verr != nil {
			err = verr
			return
		}
		q := runtimecontrol.Request{ID: "c1", Expires: time.Now().Add(time.Minute), Grant: grant, Proposal: proposal}
		err = rt.applyRuntimeSettings(q, values, map[string]any{})
	})
	return err
}

func k2SettingsRecorded(rt *Runtime) (n int) {
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			if ev.Type == runtimecontrol.EventType {
				n++
			}
		}
	})
	return n
}

// Review 15, finding 7. A switch from Codex to Claude is under way when
// the user changes Codex's model in the app's settings. No change can be
// made to Codex now: the settings change is refused with the reason, and
// nothing is recorded or published as applied.
func TestK2ALiveModelChangeDuringASwitchIsRefused(t *testing.T) {
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		rt.startRuntimeSettings()
		rt.syncRelaySettings()
	})
	t.Cleanup(func() { rt.sync(rt.stopRuntimeSettings) })
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	err := k2LiveModel(t, rt, "codex", "gpt-6.2-astra")
	if err == nil || !strings.Contains(err.Error(), "being replaced") {
		t.Errorf("the change during the switch: %v, want it refused as being replaced", err)
	}
	if n := k2SettingsRecorded(rt); n != 0 {
		t.Errorf("%d settings changes recorded as applied", n)
	}
	g.release <- struct{}{}
	recv(t, switched, "switch")
	waitRelayStarts(t, rt)
}

// Review 15, finding 8. A model change waits for the agent's turn to end;
// a message is held for the end of that turn. The turn ends and the hook
// hands the message over in the same batch: turn, idle, its arrival,
// working. The change does not start the agent again at that turn's end,
// which would cut off the turn the message began; it applies when that
// turn ends.
func TestK2AWaitingChangeDoesNotCutOffTheTurnAHeldMessageStarts(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	// The marker comes after the batch: once the watcher takes it, every
	// event before it is posted, and the sync after runs behind them.
	marker := relay.Event{Kind: relay.KindActivity, Text: "…"}
	seat.say(relay.Event{Kind: relay.KindState, State: "working"}, marker)
	rt.sync(func() {})
	if msg := k1Repick(t, rt); !strings.Contains(msg, "when the turn ends") {
		t.Fatalf("repick in a turn: %q", msg)
	}
	_, id := k2Forward(t, rt, seat, "then run the linter", "after_turn")
	seat.say(relay.Event{Kind: relay.KindTurn, Text: "Built it."}, relay.Event{Kind: relay.KindState, State: "idle"},
		relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true}, relay.Event{Kind: relay.KindState, State: "working"}, marker)
	rt.sync(func() {})
	select {
	case <-seat.gone:
		t.Fatal("the change stopped the agent in the turn the held message began")
	default:
	}
	if k1Restarting(rt) {
		t.Fatal("the change started the agent again in the turn the held message began")
	}
	seat.say(relay.Event{Kind: relay.KindTurn, Text: "Ran the linter."}, relay.Event{Kind: relay.KindState, State: "idle"})
	seat.waitGone(t)
	seat.end()
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	k1Relay(t, rt, "the new model after the second turn", func(rel *state.Relay) bool { return rel.Model == "gpt-6.2-astra" && rel.Turns == 2 })
	if got := deliveredFor(rt, id); len(got) != 1 || !got[0].OK {
		t.Fatalf("%s: %+v; want its one outcome, arrived", id, got)
	}
}

// Review 15, finding 8, its bound: the held message is not handed over at
// the turn's end (taken back unread, as the seat reports a held message
// still in the spool when the turn is over). No turn starts with it: the
// change waits no longer and applies then.
func TestK2AWaitingChangeAppliesWhenTheHeldMessageDidNotGo(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	marker := relay.Event{Kind: relay.KindActivity, Text: "…"}
	seat.say(relay.Event{Kind: relay.KindState, State: "working"}, marker)
	rt.sync(func() {})
	k1Repick(t, rt)
	_, id := k2Forward(t, rt, seat, "then run the linter", "after_turn")
	seat.say(relay.Event{Kind: relay.KindTurn, Text: "Built it."}, relay.Event{Kind: relay.KindState, State: "idle"}, marker)
	rt.sync(func() {})
	if k1Restarting(rt) {
		t.Fatal("the change started the agent again with a message on its way to it")
	}
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: false, Outcome: relay.OutcomeNotSent, Detail: "taken back unread"})
	seat.waitGone(t)
	seat.end()
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	k1Relay(t, rt, "the new model", func(rel *state.Relay) bool { return rel.Model == "gpt-6.2-astra" })
	if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
		t.Fatalf("%s: %+v; want its one outcome, not_sent", id, got)
	}
}

// A reader's suspicion on briefedBy (A18): the model stops Codex with a
// reason of its own that reads like a switch's ("handed over to claude"),
// starts Claude and sends it something. Claude was handed nothing by a
// brief: a switch away from it hands over from Claude itself, not from
// Codex. The model's calls are recorded each with its result after it, so
// its stop and start are not the neighbours a switch writes.
func TestK2AStopWithASwitchsReasonDoesNotBriefLikeASwitch(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	claude := newFakeSeat("x")
	claude.hang = true // takes the message and stays in its first turn
	seats := []*fakeSeat{newFakeSeat("Parsed the config files."), claude}
	var mu sync.Mutex
	launched := 0
	old := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		mu.Lock()
		s := seats[min(launched, len(seats)-1)]
		launched++
		mu.Unlock()
		return s, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	t.Cleanup(func() { launchRelay = old })
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now"):
			return say("Done.")
		case !strings.Contains(all, "finished a turn"):
			return hold()
		case !strings.Contains(all, "Started."):
			return reply{calls: []event.ToolCall{
				tc("relay_control", `{"action":"stop","reason":"handed over to claude"}`),
				tc("relay_start", `{"profile":"claude"}`),
				tc("relay_send", `{"relay":"claude","text":"look at the parser"}`),
			}}
		case !strings.Contains(all, "Stopped Claude"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","relay":"claude"}`)}}
		}
		return hold()
	})
	defer model.srv.Close()
	rt, _ := relayRunBatch(t, testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: "Build the config parser.", Relay: "codex"})
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	for _, ev := range evs {
		var d event.RelayEndData
		if ev.Type == event.RelayEnd && ev.Decode(&d) == nil && d.Name == "codex" && d.Reason == "handed over to claude" {
			stopped = true
		}
	}
	if got := claude.sentTexts(); !stopped || len(got) != 1 || got[0] != "look at the parser" {
		t.Fatalf("the model's stop recorded %v; Claude was sent %q", stopped, got)
	}
	fresh := &Runtime{st: state.Replay(evs)}
	rel := fresh.st.Relays["claude"]
	if rel == nil || rel.Turns != 0 {
		t.Fatalf("claude on the record: %+v", rel)
	}
	if brief := fresh.writeBrief(fresh.handoverFor(rel), nil); !strings.HasPrefix(brief, "You are taking over this work from Claude") {
		t.Errorf("a switch away from Claude would brief:\n%s", brief)
	}
}

// Review 15, finding 9. A model change restarts Codex; while it comes up a
// second change is asked (it waits for that start) and the user writes
// (held for it). The agent that start brings up is replaced at once by the
// second change: the message goes to the agent that change brings up, not
// to the one in between, and arrives there.
func TestK2AHeldSendGoesToTheAgentANewerChangeBringsUp(t *testing.T) {
	between := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	last := newFakeSeat("three")
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("zero"), between, last}, nil)
	rt := newLaunchRuntime(t)
	k1Repick(t, rt)
	g.waitEntered(t)
	var msg string
	rt.sync(func() { msg, _ = rt.repickRelay("codex", relayPick{}) })
	if !strings.Contains(msg, "once it is up") {
		t.Fatalf("second change during the start: %q", msg)
	}
	userSays(t, rt, "fix the flaky test")
	id := lastSendID(rt)
	g.release <- struct{}{}
	select {
	case <-between.entered:
		// Typed into the agent in between: it says it is ready, and the
		// waiting change replaces it as the message goes in.
		between.emit(relay.Event{Kind: relay.KindState, State: "idle"})
		between.result <- nil
		g.waitEntered(t)
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("neither the message was typed nor the second change began")
	}
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	waitFor(t, "the message in the last agent", func() bool { return len(last.sentTexts()) == 1 })
	if got := last.sentTexts(); got[0] != "fix the flaky test" {
		t.Fatalf("the last agent got %q", got)
	}
	waitFor(t, "its arrival recorded", func() bool { return len(deliveredFor(rt, id)) > 0 })
	if got := deliveredFor(rt, id); len(got) != 1 || !got[0].OK {
		t.Fatalf("%s: %+v; want its one outcome, arrived", id, got)
	}
	k1Relay(t, rt, "the second change applied", func(rel *state.Relay) bool { return rel.Model == "" })
}
