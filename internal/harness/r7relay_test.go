package harness

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
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
