package harness

import (
	"errors"
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
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply { return hold() }, nil)
	s.waitFor("the brief to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
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

// (c) Cut as in (a), and the agent is gone at the resume: nothing is typed
// and no send is recorded for it.
func TestR8AMessageCutBeforeItsSendIsNotTypedWhenTheAgentIsGone(t *testing.T) {
	sid, evs, seq, model := r8aRecord(t)
	seat := newFakeSeat("ok")
	_, after := r8aResume(t, sid, r8aCut(evs, seq), model, seat, true)
	if got := seat.sentTexts(); len(got) != 0 {
		t.Fatalf("the agent was given %q", got)
	}
	if got := r8aSends(after, seq); len(got) != 0 {
		t.Fatalf("the record has %d sends of message %d", len(got), seq)
	}
}
