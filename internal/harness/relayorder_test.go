package harness

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// gateSeat is a fake agent whose first send waits for the test to let it
// through, as a real send waits for the agent's startup screens. It stays
// usable after Kill so a send let through late cannot panic; its events
// end at the kill, as a real seat's do.
type gateSeat struct {
	*fakeSeat
	gmu     sync.Mutex
	calls   int
	entered chan string
	gate    chan struct{}
}

func newGateSeat(reply string) *gateSeat {
	return &gateSeat{fakeSeat: newFakeSeat(reply), entered: make(chan string, 16), gate: make(chan struct{})}
}

func (g *gateSeat) Send(id, text, mode string) (_ string, err error) {
	g.gmu.Lock()
	g.calls++
	first := g.calls == 1
	g.gmu.Unlock()
	g.entered <- text
	if first {
		<-g.gate
	}
	g.fakeSeat.mu.Lock()
	killed := g.fakeSeat.killed
	g.fakeSeat.mu.Unlock()
	if killed {
		return "", errAgentGone
	}
	return g.fakeSeat.Send(id, text, mode)
}

func (g *gateSeat) Kill() error {
	g.fakeSeat.mu.Lock()
	g.fakeSeat.killed = true
	g.fakeSeat.mu.Unlock()
	g.fakeSeat.Close()
	return nil
}
func (g *gateSeat) Close() {}

var errAgentGone = &gateErr{"the agent is not running"}

type gateErr struct{ s string }

func (e *gateErr) Error() string { return e.s }

func (g *gateSeat) waitEntered(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-g.entered:
		if !strings.Contains(got, want) {
			t.Fatalf("first send typed %q, want %q", oneLine(got, 80), want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("nothing was typed (want %q)", want)
	}
}

// firstWords is each text's first line, for readable order checks.
func firstWords(texts []string) []string {
	var out []string
	for _, t := range texts {
		out = append(out, oneLine(t, 40))
	}
	return out
}

// The reviewer's scenario: during a switch, instruction A waits for the
// launch; the new agent's first send is slow (the handoff brief, waiting
// for its startup screens) and correction B arrives meanwhile. The agent
// must get the brief, then A, then B.
func TestRelaySwitchTypesBriefThenBacklogThenNewInput(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("one")}, newGateSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	args := map[string]any{"action": "switch", "profile": "claude"}
	out := make(chan string, 1)
	go func() {
		text, _ := rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false)
		out <- text
	}()
	g.waitEntered(t)
	userSays(t, rt, "A: use the staging database")
	g.release <- struct{}{}
	next.waitEntered(t, "You are taking over")
	userSays(t, rt, "B: no, use the dev database")
	time.Sleep(150 * time.Millisecond) // room for B to overtake, if it can
	close(next.gate)
	select {
	case text := <-out:
		if !strings.Contains(text, "Handed over") {
			t.Fatalf("switch: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("switch never returned")
	}
	waitFor(t, "three sends typed", func() bool { return len(next.sentTexts()) == 3 })
	got := firstWords(next.sentTexts())
	if !strings.HasPrefix(got[0], "You are taking over") || got[1] != "A: use the staging database" || got[2] != "B: no, use the dev database" {
		t.Fatalf("the new agent got %q, want brief, A, B", got)
	}
	// The record says the same: brief sent before B.
	rt.sync(func() {
		var sends []string
		for _, ev := range rt.st.Events {
			if ev.Type == event.RelaySend {
				var d event.RelaySendData
				_ = ev.Decode(&d)
				sends = append(sends, d.Name+":"+oneLine(d.Text, 20))
			}
		}
		if len(sends) != 3 || !strings.HasPrefix(sends[1], "claude:You are taking") || !strings.HasPrefix(sends[2], "claude:B:") {
			t.Errorf("recorded sends %q", sends)
		}
	})
}

// A plain restart (a model change): what waited during the launch is typed
// before what arrives while it is still being typed.
func TestRelayRestartTypesBacklogBeforeNewInput(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("one")}, newGateSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	userSays(t, rt, "A: fix the flaky test")
	g.release <- struct{}{}
	next.waitEntered(t, "A: fix")
	userSays(t, rt, "B: only the one in relay_test.go")
	time.Sleep(150 * time.Millisecond)
	close(next.gate)
	waitFor(t, "two sends typed", func() bool { return len(next.sentTexts()) == 2 })
	if got := next.sentTexts(); !slices.Equal(got, []string{"A: fix the flaky test", "B: only the one in relay_test.go"}) {
		t.Fatalf("the restarted agent got %q, want A then B", got)
	}
}

// Two quick user messages and one of eagent's own to a running agent are
// typed in the order they were accepted, though the first waits.
func TestRelaySendsToRunningAgentKeepTheirOrder(t *testing.T) {
	seat := newGateSeat("one")
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, "A: rename the flag")
	seat.waitEntered(t, "A:")
	args := map[string]any{"text": "C: and update the docs"}
	toolDone := make(chan string, 1)
	go func() {
		text, _ := rt.relayTool("relay_send", argStr(args), argNum(args), argList(args), false)
		toolDone <- text
	}()
	waitFor(t, "eagent's send recorded", func() bool {
		n := 0
		rt.sync(func() {
			for _, ev := range rt.st.Events {
				if ev.Type == event.RelaySend {
					n++
				}
			}
		})
		return n == 2
	})
	userSays(t, rt, "B: actually call it --strict")
	time.Sleep(150 * time.Millisecond)
	close(seat.gate)
	select {
	case text := <-toolDone:
		if !strings.Contains(text, "typed into Codex") {
			t.Errorf("relay_send: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay_send never returned")
	}
	waitFor(t, "three sends typed", func() bool { return len(seat.sentTexts()) == 3 })
	if got := seat.sentTexts(); !slices.Equal(got, []string{"A: rename the flag", "C: and update the docs", "B: actually call it --strict"}) {
		t.Fatalf("the agent got %q, want A, C, B", got)
	}
}

// A relay stopped while sends wait in its queue: they are recorded as not
// delivered, nothing more is typed into it, and the queue's goroutine ends.
func TestRelayStopSettlesQueuedSends(t *testing.T) {
	seat := newGateSeat("one")
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	userSays(t, rt, "A: first")
	seat.waitEntered(t, "A:")
	userSays(t, rt, "B: second")
	userSays(t, rt, "C: third")
	args := map[string]any{"action": "stop"}
	if text, isErr := rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false); isErr {
		t.Fatalf("stop: %s", text)
	}
	close(seat.gate)
	waitFor(t, "the queue to end", func() bool {
		n := 0
		rt.sync(func() {
			rt.relays.mu.Lock()
			n = len(rt.relays.queues)
			rt.relays.mu.Unlock()
		})
		return n == 0
	})
	waitFor(t, "B and C recorded undelivered", func() bool {
		failed := map[string]bool{}
		rt.sync(func() {
			for _, ev := range rt.st.Events {
				if ev.Type == event.RelayDelivered {
					var d event.RelayDeliveredData
					_ = ev.Decode(&d)
					// A by its seat's word (its agent gone), B and C by the queue's.
					if !d.OK && (strings.Contains(d.Detail, "not running") || strings.Contains(d.Detail, "stopped or restarted before the message was typed")) {
						failed[d.ID] = true
					}
				}
			}
		})
		return len(failed) == 3
	})
	if got := seat.sentTexts(); len(got) != 0 {
		t.Fatalf("typed into the stopped agent: %q", got)
	}
}
