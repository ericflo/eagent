package harness

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// closingSeat is a fake agent still on its startup screens: a send waits
// until the seat is closed or killed and then fails as a real seat's does,
// with relay.ErrClosed.
type closingSeat struct {
	*fakeSeat
	entered chan string
	gone    chan struct{}
	once    sync.Once
}

func newClosingSeat(reply string) *closingSeat {
	return &closingSeat{fakeSeat: newFakeSeat(reply), entered: make(chan string, 8), gone: make(chan struct{})}
}

func (c *closingSeat) Send(id, text, mode string) (string, error) {
	c.entered <- text
	<-c.gone
	return "", relay.ErrClosed
}

func (c *closingSeat) Kill() error {
	c.once.Do(func() { close(c.gone) })
	return c.fakeSeat.Kill()
}

func (c *closingSeat) Close() {
	c.once.Do(func() { close(c.gone) })
	c.fakeSeat.Close()
}

func (c *closingSeat) waitEntered(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-c.entered:
		if !strings.Contains(got, want) {
			t.Fatalf("send typed %q, want %q", oneLine(got, 80), want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("nothing was sent (want %q)", want)
	}
}

// failedDeliveries are the sends recorded as not delivered, by id.
func failedDeliveries(rt *Runtime) map[string]event.RelayDeliveredData {
	out := map[string]event.RelayDeliveredData{}
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelayDeliveredData
			if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && !d.OK {
				out[d.ID] = d
			}
		}
	})
	return out
}

// lastSendID is the id of the newest send on the record.
func lastSendID(rt *Runtime) string {
	id := ""
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelaySendData
			if ev.Type == event.RelaySend && ev.Decode(&d) == nil {
				id = d.ID
			}
		}
	})
	return id
}

// A message waiting for the agent to be ready when a restart replaces its
// terminal goes to the new agent, like the messages queued behind it, and
// is not recorded as a failure of a relay that is running again.
func TestSendCutOffByARestartGoesToTheNewAgent(t *testing.T) {
	old, next := newClosingSeat("one"), newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, "A: rename the flag")
	old.waitEntered(t, "A:")
	id := lastSendID(rt)
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	waitFor(t, "A typed into the restarted agent", func() bool {
		got := next.sentTexts()
		return len(got) == 1 && got[0] == "A: rename the flag"
	})
	if d, failed := failedDeliveries(rt)[id]; failed {
		t.Fatalf("recorded as not delivered to the running relay: %+v", d)
	}
}

// The same during a switch: the new agent gets the handoff brief, then the
// message that was waiting.
func TestSendCutOffByASwitchGoesToTheNewAgentAfterTheBrief(t *testing.T) {
	old, next := newClosingSeat("one"), newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, "A: use the staging database")
	old.waitEntered(t, "A:")
	id := lastSendID(rt)
	out := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	if text := recv(t, out, "switch"); !strings.Contains(text, "Handed over") {
		t.Fatalf("switch: %q", text)
	}
	waitFor(t, "brief and A typed", func() bool { return len(next.sentTexts()) == 2 })
	got := firstWords(next.sentTexts())
	if !strings.HasPrefix(got[0], "You are taking over") || got[1] != "A: use the staging database" {
		t.Fatalf("the new agent got %q, want the brief, then A", got)
	}
	if d, failed := failedDeliveries(rt)[id]; failed {
		t.Fatalf("recorded as not delivered: %+v", d)
	}
}

// A message waiting for the agent when the relay is stopped is recorded as
// not delivered, at once rather than after the 40 s a send waits for an
// agent to start, and is typed into nothing.
func TestSendCutOffByAStopIsRecordedUndelivered(t *testing.T) {
	old := newClosingSeat("one")
	useGatedLaunches(t, []relaySeat{old}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, "A: rename the flag")
	old.waitEntered(t, "A:")
	id := lastSendID(rt)
	recv(t, relayToolAsync(rt, map[string]any{"action": "stop"}), "stop")
	waitFor(t, "A recorded undelivered", func() bool {
		d, failed := failedDeliveries(rt)[id]
		return failed && strings.Contains(d.Detail, "not running")
	})
}
