package harness

import (
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// r8aText is the user's second message, typed while the agent runs.
const r8aText = "now run the tests"

// r8aRecord runs a session whose agent takes the brief, then takes the
// user's second message, and returns the finished record and that
// message's seq.
func r8aRecord(t *testing.T) (sid string, evs []event.Event, seq int64, model *scripted) {
	t.Helper()
	var spoke atomic.Bool
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		// The brief is answered, so the record shows it was looked at.
		if msgs[len(msgs)-1]["role"] != "tool" && spoke.CompareAndSwap(false, true) {
			return say("Passed on.")
		}
		return hold()
	}, nil)
	s.waitFor("the brief to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.waitFor("the brief to be answered", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	s.type_(r8aText)
	s.waitFor("the second send", func() bool { return len(s.seat.sentTexts()) == 2 })
	sid, evs = cutRecord(t, s)
	for _, ev := range evs {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == r8aText {
			seq = ev.Seq
		}
	}
	if seq == 0 {
		t.Fatal("the second message is not on the record")
	}
	return sid, evs, seq, s.model
}

// r8aCut is the record up to and including the event with seq at.
func r8aCut(evs []event.Event, at int64) []event.Event {
	for i, ev := range evs {
		if ev.Seq == at {
			return evs[:i+1]
		}
	}
	return nil
}

// r8aResume resumes a cut record with a fresh seat; gone makes the agent
// one that is no longer running.
func r8aResume(t *testing.T, sid string, prefix []event.Event, model *scripted, seat *fakeSeat, gone bool) (*session, []event.Event) {
	t.Helper()
	project, path := cutWrite(t, sid, prefix)
	useFakeRelays(t, seat)
	if gone {
		attachRelay = func(relay.Spec, string, string, string, string, time.Time) (relaySeat, error) {
			return nil, errors.New("no such terminal")
		}
	}
	ui := &fakeUI{input: make(chan string, 8)}
	rt, err := Resume(testConfig(model.srv.URL), Options{Project: project, Interactive: true}, ui, path)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, rt: rt, ui: ui, seat: seat, model: model, done: make(chan int, 1)}
	ctx, cancel := contextWithCancel()
	s.cancel = cancel
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	s.settled()
	s.stop()
	after, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, after
}

// r8aSends are the relay.send events of the user's message seq.
func r8aSends(evs []event.Event, seq int64) []event.RelaySendData {
	var out []event.RelaySendData
	for _, ev := range evs {
		var d event.RelaySendData
		if ev.Type == event.RelaySend && ev.Decode(&d) == nil && d.From == "user" && d.Seq == seq {
			out = append(out, d)
		}
	}
	return out
}

// (a) Cut right after the user's message, before its send was recorded:
// the resumed run types it into the agent once and records that send.
func TestR8AMessageCutBeforeItsSendIsTypedOnceAtTheResume(t *testing.T) {
	sid, evs, seq, model := r8aRecord(t)
	seat := newFakeSeat("ok")
	_, after := r8aResume(t, sid, r8aCut(evs, seq), model, seat, false)
	if got := seat.sentTexts(); len(got) != 1 || got[0] != r8aText {
		t.Fatalf("the restored agent was given %q, want %q once", got, r8aText)
	}
	if sends := r8aSends(after, seq); len(sends) != 1 || !sends[0].Auto {
		t.Fatalf("the record has %d sends of message %d: %+v", len(sends), seq, sends)
	}
}

// (b) Cut right after the send was recorded and before its outcome: it is
// not typed again; the new seat is handed it to confirm from the
// transcript, and the record still waits on it.
func TestR8AMessageCutAfterItsSendIsNotTypedAgain(t *testing.T) {
	sid, evs, seq, model := r8aRecord(t)
	sends := r8aSends(evs, seq)
	if len(sends) != 1 {
		t.Fatalf("the full record has %d sends of message %d", len(sends), seq)
	}
	var sendSeq int64
	for _, ev := range evs {
		var d event.RelaySendData
		if ev.Type == event.RelaySend && ev.Decode(&d) == nil && d.ID == sends[0].ID {
			sendSeq = ev.Seq
		}
	}
	seat := newFakeSeat("ok")
	_, after := r8aResume(t, sid, r8aCut(evs, sendSeq), model, seat, false)
	if got := seat.sentTexts(); len(got) != 0 {
		t.Fatalf("the restored agent was given %q", got)
	}
	if got := r8aSends(after, seq); len(got) != 1 {
		t.Fatalf("the record has %d sends of message %d", len(got), seq)
	}
	if o := state.Replay(after).SendOutcome(sends[0].ID); o != state.SendWaiting {
		t.Fatalf("the send's outcome is %q, want it still waiting", o)
	}
}

// (c) Cut as in (a), and the agent is gone at the resume: nothing is typed,
// no send is recorded for it, and the model is not told it reached the
// agent.
func TestR8AMessageCutBeforeItsSendIsNotTypedWhenTheAgentIsGone(t *testing.T) {
	sid, evs, seq, model := r8aRecord(t)
	before := len(model.requests(testModel))
	seat := newFakeSeat("ok")
	_, after := r8aResume(t, sid, r8aCut(evs, seq), model, seat, true)
	if got := seat.sentTexts(); len(got) != 0 {
		t.Fatalf("the agent was given %q", got)
	}
	if got := r8aSends(after, seq); len(got) != 0 {
		t.Fatalf("the record has %d sends of message %d", len(got), seq)
	}
	reqs := model.requests(testModel)
	if len(reqs) <= before {
		t.Fatal("the resumed session made no model call")
	}
	tail := r8bTail(reqs[before])
	if strings.Contains(tail, "that it reached") || !strings.Contains(tail, "did not go") {
		t.Fatalf("the first call after the resume was told:\n%s", tail)
	}
}

// r8bTail is the last user text of a request the model received.
func r8bTail(q request) string {
	msgs := make([]map[string]any, len(q.Messages))
	for i, m := range q.Messages {
		_ = json.Unmarshal(m, &msgs[i])
	}
	return lastUserText(msgs)
}

// r8bStart starts a session whose agent is seat (fake is the fakeSeat in
// it) and takes the brief, then types a message and returns the first
// request the model received after it.
func r8bStart(t *testing.T, seat relaySeat, fake *fakeSeat, before func()) string {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	oldLaunch := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		return seat, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	t.Cleanup(func() { launchRelay = oldLaunch })
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	ui := &fakeUI{input: make(chan string, 16)}
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Interactive: true, Prompt: brief}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := contextWithCancel()
	s := &session{t: t, rt: rt, ui: ui, seat: fake, model: model, cancel: cancel, done: make(chan int, 1)}
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	s.settled()
	before()
	// The look waits this long after the message, as it does in use, so
	// what the agent does with it in that time is on the record by then.
	setFor(t, &wakeSettle, 100*time.Millisecond)
	n := len(model.requests(testModel))
	s.type_(r8aText)
	s.waitFor("the look at the message", func() bool { return len(model.requests(testModel)) > n })
	return r8bTail(model.requests(testModel)[n])
}

// The agent's transcript showed the message before the look: the model is
// asked to say it reached the agent.
func TestR8BAnArrivedMessageIsAcknowledgedAsReached(t *testing.T) {
	seat := newFakeSeat("ok")
	seat.hang = true
	tail := r8bStart(t, seat, seat, func() {})
	if !strings.Contains(tail, "Say in one short line that it reached Codex") {
		t.Fatalf("the look after an arrived message was told:\n%s", tail)
	}
}

// Typed and not yet shown: the model is told it waits to be taken up, not
// that it reached the agent.
func TestR8BAMessageNotYetConfirmedIsNotCalledReached(t *testing.T) {
	seat := &quietSeat{fakeSeat: newFakeSeat("ok")}
	tail := r8bStart(t, seat, seat.fakeSeat, func() {})
	if strings.Contains(tail, "that it reached") || !strings.Contains(tail, "waits to be taken up") {
		t.Fatalf("the look after a send still waiting was told:\n%s", tail)
	}
}

// The seat fails before typing, within the wake-settle interval: the look
// says once that it did not go and gives one recovery instruction.
func TestR8BAMessageThatDidNotGoIsDescribedOnce(t *testing.T) {
	seat := newFakeSeat("ok")
	seat.hang = true
	tail := r8bStart(t, seat, seat, func() { seat.setSendErr(errors.New("the terminal refused the text")) })
	if strings.Contains(tail, "that it reached") {
		t.Fatalf("the look after a failed send says it reached the agent:\n%s", tail)
	}
	if strings.Count(tail, "did not go") != 1 || strings.Count(tail, "Send it again") != 1 || strings.Contains(tail, "could not be confirmed") {
		t.Fatalf("the look after a failed send does not describe it once:\n%s", tail)
	}
}
