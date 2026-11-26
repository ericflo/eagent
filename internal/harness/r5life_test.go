package harness

import (
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
