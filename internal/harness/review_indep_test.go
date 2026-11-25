package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

func relayToolAsync(rt *Runtime, args map[string]any) <-chan string {
	out := make(chan string, 1)
	go func() {
		text, _ := rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false)
		out <- text
	}()
	return out
}

func recv(t *testing.T, ch <-chan string, what string) string {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never returned", what)
	}
	return ""
}

// A relay the user stopped (/stop-relay) can be brought back with /restart
// on its own conversation, as before restarts ran off the loop: the restart
// must not be taken for "stopped while it started again".
func TestIndepRestartAfterStopRevivesTheRelay(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("one")}, newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)

	stop := map[string]any{"action": "stop", "relay": "codex"}
	if text := recv(t, relayToolAsync(rt, stop), "stop"); !strings.Contains(text, "Stopped") {
		t.Fatalf("stop: %q", text)
	}
	out := relayToolAsync(rt, map[string]any{"action": "restart"})
	g.waitEntered(t)
	g.release <- struct{}{}
	text := recv(t, out, "restart")
	if strings.Contains(text, "Could not restart") {
		t.Errorf("restart of a stopped relay refused: %q", text)
	}
	if next.wasKilled() {
		t.Errorf("the agent the restart launched was killed")
	}
	rt.sync(func() {
		if rel := rt.st.Relays["codex"]; rel == nil || !rel.Active() {
			t.Errorf("relay after restart: %+v", rel)
		}
	})
}

// Stop session stops every agent. One that a switch was launching when the
// user pressed it must not come up afterwards and be left running.
func TestIndepStopSessionDuringSwitchLeavesNoAgent(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("one")}, newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	out := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	rt.sync(func() { rt.stopSession("stopped from the app") })
	g.release <- struct{}{}
	recv(t, out, "switch")
	waitRelayStarts(t, rt)
	rt.sync(func() {
		for _, rel := range rt.st.ActiveRelays() {
			t.Errorf("after Stop session, %s is on the record as running", rel.Name)
		}
	})
	// The agent is stopped on a goroutine of its own when the session
	// lives on, so the loop does not wait for it.
	waitFor(t, "the agent the switch launched to be stopped after Stop session", next.wasKilled)
}

// /stop-relay while a switch is launching: the user stopped the agent, so
// the one taking over must not start in its place.
func TestIndepStopDuringSwitchStopsTheHandover(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("one")}, newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	out := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	recv(t, relayToolAsync(rt, map[string]any{"action": "stop", "relay": "codex", "reason": "the user stopped it"}), "stop")
	g.release <- struct{}{}
	recv(t, out, "switch")
	waitRelayStarts(t, rt)
	rt.sync(func() {
		for _, rel := range rt.st.ActiveRelays() {
			t.Errorf("after /stop-relay during a switch, %s runs", rel.Name)
		}
	})
	// The agent is stopped on a goroutine of its own when the session
	// lives on, so the loop does not wait for it.
	waitFor(t, "the agent the switch launched to be stopped after the user stopped the relay", next.wasKilled)
}

// Stop session while eagent's relay_start is launching.
func TestIndepStopSessionDuringStartLeavesNoAgent(t *testing.T) {
	first, next := newFakeSeat("one"), newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{first, next}, nil)
	rt := newLaunchRuntime(t)
	out := make(chan string, 1)
	go func() {
		args := map[string]any{"profile": "claude"}
		text, _ := rt.relayTool("relay_start", argStr(args), argNum(args), argList(args), false)
		out <- text
	}()
	g.waitEntered(t)
	rt.sync(func() { rt.stopSession("stopped from the app") })
	g.release <- struct{}{}
	recv(t, out, "relay_start")
	rt.sync(func() {
		var ended bool
		for _, ev := range rt.st.Events {
			if ev.Type == event.RelayEnd {
				ended = true
			}
			if ended && ev.Type == event.RelayStart {
				t.Errorf("a relay was recorded started after Stop session")
			}
		}
	})
	// The agent is stopped on a goroutine of its own when the session
	// lives on, so the loop does not wait for it.
	waitFor(t, "the agent relay_start launched to be stopped after Stop session", next.wasKilled)
}
