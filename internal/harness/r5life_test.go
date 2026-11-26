package harness

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/clientcaps"
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

// With no agent running, a message from the phone or the web that starts
// with a slash and is not one of eagent's commands ("/tmp/out.log is
// empty", an agent's own command) is the user's words for eagent: it is on
// the record and owed an answer, where it used to be dropped with a note
// only a terminal could show. In a terminal, it is still an unknown command.
func TestASlashMessageFromThePhoneWithNoAgentRunningIsKept(t *testing.T) {
	useFakeRelays(t, newFakeSeat("one"))
	rt := newLaunchRuntime(t)
	if out := relayToolNamed(t, rt, "relay_control", map[string]any{"action": "stop"}); !strings.Contains(out, "Stopped") {
		t.Fatalf("stop: %s", out)
	}
	const words = "/tmp/out.log is empty, why?"
	rt.sync(func() {
		rt.handleInbox(InboxMessage{Type: "message", Text: words, From: "finalechat", PhoneID: "m1"})
		rt.handleInbox(InboxMessage{Type: "message", Text: "/compact", From: "web"})
		rt.onInput("/frobnicate")
	})
	var got []string
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.UserMessageData
			if ev.Type == event.UserMessage && ev.Decode(&d) == nil {
				got = append(got, d.Text)
			}
		}
	})
	if !slices.Equal(got, []string{words, "/compact"}) {
		t.Fatalf("user messages on the record: %q", got)
	}
	// One of eagent's own commands is still carried out.
	rt.sync(func() {
		n := len(rt.st.Events)
		rt.handleInbox(InboxMessage{Type: "message", Text: "/status", From: "web"})
		for _, ev := range rt.st.Events[n:] {
			if ev.Type == event.UserMessage {
				t.Error("/status was recorded as words")
			}
		}
	})
}

// relayTurned waits until the relay named has finished turns turns.
func relayTurned(t *testing.T, rt *Runtime, name string, turns int) {
	t.Helper()
	waitFor(t, name+"'s turn on the record", func() bool {
		n := -1
		rt.sync(func() {
			if rel := rt.st.Relays[name]; rel != nil && rel.Active() {
				n = rel.Turns
			}
		})
		return n == turns
	})
}

// A brief quotes what the user told the agents whose work it hands over.
// An earlier agent that had the same name and was stopped, not handed
// over, did other work: what the user told it is not quoted.
func TestABriefLeavesOutWhatWasSaidToAStoppedAgentOfTheSameName(t *testing.T) {
	const second = "only touch the docs directory"
	first, fresh, claude := newFakeSeat("one"), newFakeSeat("two"), newFakeSeat("three")
	useSeatList(t, first, fresh, claude)
	rt := newLaunchRuntime(t)
	userSays(t, rt, r4Task)
	r4CodexTurned(t, rt)
	if out := relayToolNamed(t, rt, "relay_control", map[string]any{"action": "stop"}); !strings.Contains(out, "Stopped") {
		t.Fatalf("stop: %s", out)
	}
	if out := relayToolNamed(t, rt, "relay_start", map[string]any{"profile": "codex"}); !strings.Contains(out, "Started") {
		t.Fatalf("start: %s", out)
	}
	userSays(t, rt, second)
	relayTurned(t, rt, "codex", 1)
	recv(t, relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"}), "switch")
	waitFor(t, "Claude's brief typed", func() bool { return len(claude.sentTexts()) >= 1 })
	brief := claude.sentTexts()[0]
	if !strings.Contains(brief, second) {
		t.Errorf("the work handed over is missing from the brief:\n%s", brief)
	}
	if strings.Contains(brief, r4Task) {
		t.Errorf("the brief quotes what the user told an earlier, stopped agent named codex:\n%s", brief)
	}
}

// A switch back to an agent of a name used before is the same work: Codex,
// then Claude, then Codex again, then pi. pi's brief still quotes what the
// user told the first Codex.
func TestASwitchBackKeepsTheFirstAgentsInstructionsInTheBrief(t *testing.T) {
	first, claude, back, pi := newFakeSeat("one"), newFakeSeat("two"), newFakeSeat("three"), newFakeSeat("four")
	useSeatList(t, first, claude, back, pi)
	rt := newLaunchRuntime(t)
	userSays(t, rt, r4Task)
	r4CodexTurned(t, rt)
	recv(t, relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"}), "switch to Claude")
	relayTurned(t, rt, "claude", 1)
	recv(t, relayToolAsync(rt, map[string]any{"action": "switch", "profile": "codex", "relay": "claude"}), "switch back to Codex")
	relayTurned(t, rt, "codex", 1)
	recv(t, relayToolAsync(rt, map[string]any{"action": "switch", "profile": "pi", "relay": "codex"}), "switch to pi")
	waitFor(t, "pi's brief typed", func() bool { return len(pi.sentTexts()) >= 1 })
	if brief := pi.sentTexts()[0]; !strings.Contains(brief, r4Task) {
		t.Errorf("pi's brief lost the first Codex's instructions:\n%s", brief)
	}
}

// A chain of quick switches that comes back to a name it passed through
// (Codex, Claude, Codex again, none of them past its brief, then pi) holds
// the name twice: the first Codex's instructions are still the work.
func TestAQuickChainThroughTheSameNameTwiceKeepsTheTask(t *testing.T) {
	typed, said := r4Chain(t, []string{"claude", "codex", "pi"})
	r4CheckFinal(t, typed, "Claude")
	r4CheckSaid(t, said)
}

// "Until 10:00" from the user is 10:00 where the user is. The phone and
// the web say which zone that is with each message; the model's
// relay_autopilot reads the time there, as the /autopilot command does,
// and not in the zone of the machine eagent runs on.
func TestAnAutopilotDeadlineIsReadInTheUsersTimeZone(t *testing.T) {
	zone := "Asia/Manila"
	if time.Local.String() == zone {
		zone = "America/Los_Angeles"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Skip(err)
	}
	useFakeRelays(t, newFakeSeat("one"))
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "keep it going until 10am", Source: "finalechat", Held: "eagent", Client: &clientcaps.Caps{Source: "finalechat", Timezone: zone}}))
	})
	out := relayToolNamed(t, rt, "relay_autopilot", map[string]any{"until": "10:00"})
	var until string
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelayAutopilotData
			if ev.Type == event.RelayAutopilot && ev.Decode(&d) == nil {
				until = d.Until
			}
		}
	})
	at, err := time.Parse(time.RFC3339, until)
	if err != nil {
		t.Fatalf("autopilot deadline %q (%s): %v", until, out, err)
	}
	if h, m := at.In(loc).Hour(), at.In(loc).Minute(); h != 10 || m != 0 {
		t.Errorf("the deadline is %s, %02d:%02d in %s, want 10:00 there", at, h, m, zone)
	}
	if !strings.Contains(out, "10:00") {
		t.Errorf("the tool does not say the deadline as the user gave it: %s", out)
	}
}
