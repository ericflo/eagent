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

func (p *pastingSeat) Send(id, text, mode string) (string, error) {
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
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
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
// is stopped is left as it was by the stop. When that relay is restarted on
// its own conversation, the new process waits on nothing: the send is
// reported unconfirmed, once, not left waiting for ever.
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
	if got := deliveredFor(rt, typed); len(got) != 0 {
		t.Fatalf("the stop reported the send: %+v", got)
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
