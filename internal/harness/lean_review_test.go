package harness

// What the review of the leanness pass's removals proved, each fixed.

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// eagent sends Codex "run the tests" itself and then restarts it. The
// restart settles the send as unconfirmed; the old seat's watcher then
// hands over the arrival it had read, and the record ends "arrived". The
// model, which was told the older word, is told the newer one too: its
// conversation says the message arrived after all, and the wake for the
// unconfirmed send no longer tells it to send again.
func TestALateArrivalAfterARestartIsToldToTheModel(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	if out, isErr := rt.relayTool("relay_send", argStr(map[string]any{"text": "run the tests"}), argNum(nil), argList(nil), false); isErr {
		t.Fatalf("relay_send: %s", out)
	}
	id := lastSendID(rt)
	k1QueuesIdle(t, rt)
	g.release <- struct{}{} // the new agent comes up at once
	restarted := relayToolAsync(rt, map[string]any{"action": "restart"})
	seat.waitGone(t)
	waitRelayStarts(t, rt) // the restart settled the send (unconfirmed)
	recv(t, restarted, "restart")
	if o := k2Outcome(rt, id); o != state.SendUnconfirmed {
		t.Fatalf("%s after the restart: %q; want unconfirmed (the setup)", id, o)
	}
	var before string
	rt.sync(func() { before = rt.attentionTail("Codex") })
	if !strings.Contains(before, "could not be confirmed") {
		t.Fatalf("setup: the wake before the arrival says %q", before)
	}
	// The old seat's watcher now hands over the arrival it had read.
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true})
	seat.end()
	waitFor(t, "the arrival recorded", func() bool { return k2Outcome(rt, id) == state.SendArrived })
	var tail, view string
	rt.sync(func() {
		tail = rt.attentionTail("Codex")
		b, _ := json.Marshal(rt.st.View(nil))
		view = string(b)
	})
	if strings.Contains(tail, "send it again") || !strings.Contains(tail, "arrived after all") {
		t.Errorf("the record says %s arrived, but eagent's wake says: %q", id, tail)
	}
	if !strings.Contains(view, "("+id+") arrived after all") {
		t.Errorf("the conversation does not say that %s arrived after all", id)
	}
}

// Claude is mid-turn and takes a message typed meanwhile into its own
// queue; its queue line marks it arrived, so nothing is awaited. A model
// change asked now waits for the turn. When the turn ends, Claude's queue
// starts the message: the change is not applied in those moments
// (repickGrace), and waits for the turn the message starts.
func TestAWaitingChangeLetsAMessageQueuedDuringTheTurnRun(t *testing.T) {
	setFor(t, &repickGrace, 80*time.Millisecond)
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	seat.say(relay.Event{Kind: relay.KindState, State: "working"})
	if out, isErr := rt.relayTool("relay_send", argStr(map[string]any{"text": "then run the linter"}), argNum(nil), argList(nil), false); isErr {
		t.Fatalf("relay_send: %s", out)
	}
	id := lastSendID(rt)
	k1QueuesIdle(t, rt)
	seat.say(relay.Event{Kind: relay.KindDelivered, SendID: id, OK: true}) // Claude's queue line
	msg, isErr := rt.relayTool("relay_control", argStr(map[string]any{"action": "model", "model": "gpt-6.2-astra"}), argNum(nil), argList(nil), false)
	if isErr || !strings.Contains(msg, "when the turn ends") {
		t.Fatalf("model: %s (want it to wait for the turn: the setup)", msg)
	}
	restarting := func() (yes bool) {
		rt.sync(func() { yes = rt.relays.launch("codex") != nil })
		return yes
	}
	// The turn that was running ends, and the queue starts the message.
	seat.say(relay.Event{Kind: relay.KindTurn, Text: "tests pass"}, relay.Event{Kind: relay.KindState, State: "idle"})
	if restarting() {
		t.Fatal("the agent is restarted at the end of the turn the message arrived during")
	}
	seat.say(relay.Event{Kind: relay.KindState, State: "working"})
	if k2Until(200*time.Millisecond, restarting) {
		t.Fatal("the agent is restarted in the turn its queued message started")
	}
	// That turn ends: the change is applied once the grace is over.
	seat.say(relay.Event{Kind: relay.KindTurn, Text: "lint is clean"}, relay.Event{Kind: relay.KindState, State: "idle"})
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	k1Relay(t, rt, "the change applied", func(rel *state.Relay) bool { return rel.Model == "gpt-6.2-astra" })
}

// The same for a change asked right after a turn's end, with no change
// waiting before: it is not applied on the spot, when Claude's queue may be
// about to start a message, but once the agent has plainly stayed between
// turns.
func TestAChangeAskedRightAfterATurnsEndWaitsAMoment(t *testing.T) {
	setFor(t, &repickGrace, 80*time.Millisecond)
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	seat.say(relay.Event{Kind: relay.KindState, State: "working"}, relay.Event{Kind: relay.KindTurn, Text: "tests pass"}, relay.Event{Kind: relay.KindState, State: "idle"})
	k1Relay(t, rt, "the turn's end recorded", func(rel *state.Relay) bool { return rel.State == "idle" && rel.Turns == 1 })
	var msg string
	var err error
	var restarting bool
	rt.sync(func() {
		msg, err = rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""})
		restarting = rt.relays.launch("codex") != nil
	})
	if err != nil || restarting || !strings.Contains(msg, "in a few seconds") {
		t.Fatalf("a change asked right after the turn's end: %q, %v, restarting at once: %v; want it to wait a moment and say so", msg, err, restarting)
	}
	g.waitEntered(t) // still between turns after the grace: applied
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	k1Relay(t, rt, "the change applied", func(rel *state.Relay) bool { return rel.Model == "gpt-6.2-astra" })
}

// A second message kept for eagent that arrives while a turn runs, after the
// turn already sent back a hold on the first one, is owed its own send-back
// in that turn: without it the hold on it stands with the model never told
// that the message went nowhere.
func TestASecondKeptMessageInOneTurnGetsItsOwnSendBack(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	reminders := 0
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(lastText(msgs), "Holding is not an answer") {
			reminders++
			if reminders == 1 {
				// Looks at something; the user writes again meanwhile.
				return reply{calls: []event.ToolCall{tc("session_search", `{"query":"release"}`)}, block: release}
			}
			return say("Both answered.")
		}
		return hold()
	}, nil)
	s.type_("@e what is Codex up to?")
	s.waitFor("the first send-back", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return reminders == 1
	})
	s.type_("@e and is the release tagged?")
	s.waitFor("the second message on the record", func() bool {
		n := 0
		for _, ev := range s.events(event.UserMessage) {
			var d event.UserMessageData
			if ev.Decode(&d) == nil && d.Held != "" {
				n++
			}
		}
		return n == 2
	})
	close(release)
	s.settled()
	mu.Lock()
	defer mu.Unlock()
	if reminders != 2 {
		t.Fatalf("a hold on the second message kept for eagent was sent back %d times in all (want 2: one per message); said %q", reminders, s.ui.said())
	}
}

// A record written by an earlier head of this branch holds answer.owed
// events: it loads, replays and resumes, and the event shows nowhere.
func TestAnOldRecordWithAnswerOwedResumes(t *testing.T) {
	var mu sync.Mutex
	held := 0
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(allText(msgs), "answer.owed") || strings.Contains(allText(msgs), `"tries"`) {
			t.Errorf("the old event reached a prompt")
		}
		if strings.Contains(allText(msgs), "is the release tagged") {
			return say("Not yet.")
		}
		held++
		return hold()
	}, nil)
	s.type_("@e what is Codex up to?")
	s.waitFor("the two holds", func() bool { mu.Lock(); defer mu.Unlock(); return held >= 2 })
	s.settled()
	var seq int64
	for _, ev := range s.events(event.UserMessage) {
		seq = ev.Seq
	}
	s.cancel()
	coreExit(s)
	path, project := s.rt.sess.Path, s.rt.opts.Project
	sess, _, err := store.Open(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []map[string]any{{"seq": seq, "tries": 1}, {"seq": seq, "tries": 1, "started": true}} {
		if _, err := sess.Append(event.New("answer.owed", event.ActorEagent, data)); err != nil {
			t.Fatal(err)
		}
	}
	sess.Close()
	s2, _ := coreResume(t, path, project, s.model, newFakeSeat("unused"))
	s2.settled()
	s2.type_("@e and is the release tagged?")
	s2.waitFor("the answer after the resume", func() bool { return s2.ui.messageCount() == 1 })
}

// A message kept for eagent that a turn looked at and then lost to a failed
// call is taken up by the retried look; a hold there is the model's first
// silence on it and is sent back once, as any hold on such a message is.
func TestAHoldAfterAFailedCallOnAKeptMessageIsSentBack(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0})
	var mu sync.Mutex
	phase, reminders := 0, 0
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(lastText(msgs), "Holding is not an answer"):
			reminders++
			return say("Codex is idle.")
		case phase == 0 && strings.Contains(allText(msgs), "what is Codex up to"):
			phase = 1
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"codex"}`)}}
		case phase == 1 && msgs[len(msgs)-1]["role"] == "tool":
			phase = 2
			return reply{status: 400, body: `{"error":{"message":"malformed request"}}`}
		}
		return hold()
	}, nil)
	s.type_("@e what is Codex up to?")
	s.waitFor("the failed call", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return phase == 2
	})
	s.settled()
	mu.Lock()
	defer mu.Unlock()
	var ends []string
	for _, ev := range s.events(event.TurnEnd) {
		var d event.TurnData
		_ = ev.Decode(&d)
		ends = append(ends, d.Reason)
	}
	if len(ends) == 0 || ends[0] != "error" || s.model.count(testModel) < 3 { // the look, the failed call, the retried look
		t.Fatalf("not the case under test: turn ends %q, %d calls", ends, s.model.count(testModel))
	}
	if reminders != 1 || s.ui.messageCount() == 0 {
		t.Fatalf("the hold on the kept message after the failed call was sent back %d times (want 1); said %q; turn ends %q; %d calls", reminders, s.ui.said(), ends, s.model.count(testModel))
	}
}
