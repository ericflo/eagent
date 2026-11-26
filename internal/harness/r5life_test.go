package harness

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// useSeatList makes the session's agent starts take the given seats in
// order, one per start; a start past the end of the list fails the test.
func useSeatList(t *testing.T, seats ...relaySeat) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	old, oldAttach := launchRelay, attachRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		mu.Lock()
		defer mu.Unlock()
		if n >= len(seats) {
			t.Errorf("start %d: no seat left for it", n+1)
			return nil, relay.Started{}, errEnding
		}
		s := seats[n]
		n++
		return s, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	attachRelay = func(relay.Spec, string, string, string, string, time.Time) (relaySeat, error) {
		return nil, errEnding
	}
	t.Cleanup(func() { launchRelay, attachRelay = old, oldAttach })
}

// A closing look that starts a new agent is not the session's last word:
// once that agent's work is done and it is stopped, the session asks for a
// closing report again, for the new work, instead of ending in silence.
func TestAgentStartedByTheClosingLookGetsAClosingLookOfItsOwn(t *testing.T) {
	first, second := newFakeSeat("Merged #42."), newFakeSeat("Changelog updated.")
	useSeatList(t, first, second)
	var mu sync.Mutex
	closing := 0
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		last := lastText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(last, "The session is ending now"):
			mu.Lock()
			closing++
			n := closing
			mu.Unlock()
			if n == 1 {
				return reply{calls: []event.ToolCall{tc("relay_start", `{"profile":"codex"}`), tc("relay_send", `{"text":"also update the changelog"}`)}}
			}
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Merged #42 and updated the changelog.","important":true}`)}}
		case strings.Contains(last, "finished a turn"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"done"}`)}}
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, nil, model, nil)
	select {
	case code := <-s.done:
		s.done <- code
	case <-time.After(10 * time.Second):
		for _, ev := range s.events() {
			t.Logf("%d %s %s", ev.Seq, ev.Type, clipRunes(string(ev.Data), 200))
		}
		t.Fatal("the session never ended")
	}
	// The loop has ended: its state is read directly.
	starts := 0
	for _, ev := range s.rt.st.Events {
		if ev.Type == event.RelayStart {
			starts++
		}
	}
	if n := starts; n != 2 {
		t.Fatalf("%d agents started, want 2", n)
	}
	if n := audit2CountLast(model, "The session is ending now"); n != 2 {
		t.Fatalf("%d closing looks, want one for each agent's work", n)
	}
}

// relayToolNamed runs one relay tool off the loop goroutine, as a model's
// call does, and returns what it said.
func relayToolNamed(t *testing.T, rt *Runtime, tool string, args map[string]any) string {
	t.Helper()
	out := make(chan string, 1)
	go func() {
		text, _ := rt.relayTool(tool, argStr(args), argNum(args), argList(args), false)
		out <- text
	}()
	return recv(t, out, tool)
}

// A model change that waited for an agent's turn to end belongs to that
// agent. Once it is stopped, a new agent that takes the name is not killed
// and started again with the old agent's waiting change when it goes idle.
func TestAWaitingModelChangeDiesWithTheAgentItWasFor(t *testing.T) {
	first, fresh := newFakeSeat("one"), newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{first, fresh}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() { rt.st.Relays["codex"].State = "working" })
	rt.sync(func() {
		if msg, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil || !strings.Contains(msg, "when the turn ends") {
			t.Errorf("repick: %q %v", msg, err)
		}
	})
	if out := relayToolNamed(t, rt, "relay_control", map[string]any{"action": "stop"}); !strings.Contains(out, "Stopped") {
		t.Fatalf("stop: %s", out)
	}
	start := make(chan string, 1)
	go func() {
		args := map[string]any{"profile": "codex"}
		text, _ := rt.relayTool("relay_start", argStr(args), argNum(args), argList(args), false)
		start <- text
	}()
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, start, "relay_start")
	waitRelayStarts(t, rt)
	fresh.emit(relay.Event{Kind: relay.KindState, State: "working"}, relay.Event{Kind: relay.KindState, State: "idle"})
	waitFor(t, "the new agent idle on the record", func() bool {
		idle := false
		rt.sync(func() { idle = rt.st.Relays["codex"].State == "idle" })
		return idle
	})
	rt.sync(func() {
		if rt.relays.launch("codex") != nil {
			t.Error("the new agent is being started again with the stopped agent's model change")
		}
		if len(rt.relayRepick) != 0 || rt.relayRoutes()["codex"].Waiting {
			t.Errorf("the stopped agent's change still waits: %v", rt.relayRepick)
		}
	})
	if fresh.wasKilled() {
		t.Error("the new agent was killed")
	}
}

// A send that a switch passed on to the new agent is the new agent's on the
// record. When typing it into that agent fails, the failure is recorded
// under the new agent's name, so the new agent stops waiting for it.
func TestASendMovedByASwitchFailsUnderTheAgentItWasMovedTo(t *testing.T) {
	old := newClosingSeat("one")
	next := newFakeSeat("two")
	next.sendErr = errors.New("the terminal went away")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	sent := make(chan string, 1)
	go func() {
		args := map[string]any{"text": "also run the linter"}
		text, _ := rt.relayTool("relay_send", argStr(args), argNum(args), argList(args), false)
		sent <- text
	}()
	old.waitEntered(t, "also run the linter")
	sw := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, sw, "switch")
	if out := recv(t, sent, "relay_send"); !strings.Contains(out, "Could not send") {
		t.Fatalf("relay_send: %s", out)
	}
	var id string
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelayMovedData
			if ev.Type == event.RelayMoved && ev.Decode(&d) == nil && d.Text == "also run the linter" {
				id = d.ID
			}
		}
	})
	if id == "" {
		t.Fatal("the send was not moved to the new agent")
	}
	waitFor(t, "the failure on the record", func() bool { _, ok := failedDeliveries(rt)[id]; return ok })
	if d := failedDeliveries(rt)[id]; d.Name != "claude" {
		t.Errorf("the failure is recorded under %q, not the agent the send was moved to", d.Name)
	}
	rt.sync(func() {
		if _, waits := rt.st.Relays["claude"].Undelivered[id]; waits {
			t.Error("the new agent still waits for a send that failed")
		}
	})
}
