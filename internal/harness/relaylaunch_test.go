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
)

// lingerSeat is a fake agent whose news can still arrive after it was
// killed: what it said before its events ended at the kill, as a real
// seat's watcher closes them once it is killed, is still read after. A
// Close alone (the session's end) leaves them open.
type lingerSeat struct{ *fakeSeat }

func (l *lingerSeat) Kill() error { return l.fakeSeat.Kill() }
func (l *lingerSeat) Close()      {}

// gatedLaunches hands out seats in order. The first launch (the session's
// own start) is immediate; each later one waits for release and then
// returns the next seat, or fails with the next error when its seat is nil.
type gatedLaunches struct {
	mu      sync.Mutex
	seats   []relaySeat
	errs    []error
	specs   []relay.Spec
	entered chan int
	release chan struct{}
}

func useGatedLaunches(t *testing.T, seats []relaySeat, errs []error) *gatedLaunches {
	t.Helper()
	g := &gatedLaunches{seats: seats, errs: errs, entered: make(chan int, 8), release: make(chan struct{}, 8)}
	old := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		g.mu.Lock()
		n := len(g.specs)
		g.specs = append(g.specs, spec)
		g.mu.Unlock()
		if n > 0 {
			g.entered <- n
			<-g.release
		}
		started := relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}
		if seat := g.seats[n]; seat != nil {
			return seat, started, nil
		}
		return nil, relay.Started{}, g.errs[n]
	}
	t.Cleanup(func() { launchRelay = old })
	return g
}

func (g *gatedLaunches) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the launch never began")
	}
}

// loopResponds checks the loop goroutine runs other work while a launch
// waits.
func loopResponds(t *testing.T, rt *Runtime) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		rt.sync(func() {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop goroutine is held by a relay launch")
	}
}

// userSays routes a user message the way the loop does when it arrives.
func userSays(t *testing.T, rt *Runtime, text string) {
	t.Helper()
	rt.sync(func() {
		d := event.UserMessageData{Text: text}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("message not routed to the agent: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
}

func newLaunchRuntime(t *testing.T) *Runtime {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	relayServeLoop(t, rt)
	rt.sync(func() { rt.st.Relays["codex"].State = "idle" })
	return rt
}

// A model change restarts the agent without holding the loop goroutine;
// a message sent meanwhile is typed into the new agent, never the old one;
// and news the killed agent still had queued is not taken for the new
// one's (a late "exited" marked the restarted relay exited).
func TestRelayRestartRunsOffTheLoop(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("one")}, newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)

	var msg string
	var err error
	rt.sync(func() {
		// The old agent's late news: said before the kill ends its events,
		// read once the restart has taken its seat out of use.
		old.emit(relay.Event{Kind: relay.KindState, State: "exited", Detail: "pane gone"})
		msg, err = rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""})
	})
	if err != nil || !strings.Contains(msg, "starting again") {
		t.Fatalf("repick: %q %v", msg, err)
	}
	g.waitEntered(t)
	loopResponds(t, rt)
	if !old.wasKilled() {
		t.Fatal("the old agent was not stopped before the new one launched")
	}
	userSays(t, rt, "also fix the flaky test")
	if len(old.sentTexts()) != 0 {
		t.Fatalf("typed into the stopped agent: %q", old.sentTexts())
	}
	// The change still shows as waiting while the agent starts.
	rt.sync(func() {
		if got := rt.relayRoutes()["codex"]; !got.Waiting || got.Model != "gpt-6.2-astra" {
			t.Errorf("while starting: %+v", got)
		}
	})
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	waitFor(t, "the held message in the new agent", func() bool {
		return slices.Contains(next.sentTexts(), "also fix the flaky test")
	})
	rt.sync(func() {
		rel := rt.st.Relays["codex"]
		if _, waiting, _ := rt.nextPick("codex"); rel.Model != "gpt-6.2-astra" || waiting {
			t.Errorf("after the restart: model %q, waiting %v", rel.Model, rt.relayNext)
		}
	})

	rt.sync(func() {
		if st := rt.st.Relays["codex"].State; st == "exited" {
			t.Errorf("the killed seat's exit marked the new relay %s", st)
		}
	})
}

// A restart that cannot start leaves no relay that claims to run: it is
// exited with the reason (which wakes eagent), the waiting change is gone,
// and what was sent meanwhile is recorded as not delivered.
func TestRelayRestartThatFailsIsExited(t *testing.T) {
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), nil}, []error{nil, errors.New("codex is not installed (not on PATH)")})
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	userSays(t, rt, "keep going")
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	rt.sync(func() {
		rel := rt.st.Relays["codex"]
		if rel.State != "exited" || !strings.Contains(rel.Detail, "not on PATH") {
			t.Errorf("relay after a failed restart: %s %q", rel.State, rel.Detail)
		}
		if _, ok, _ := rt.nextPick("codex"); ok || rt.relayRoutes()["codex"].Waiting {
			t.Errorf("the change still waits: %v", rt.relayNext)
		}
		if len(rel.Undelivered) != 0 {
			t.Errorf("held send not settled: %v", rel.Undelivered)
		}
		var failed, errored bool
		for _, ev := range rt.st.Events {
			switch ev.Type {
			case event.RelayDelivered:
				var d event.RelayDeliveredData
				_ = ev.Decode(&d)
				failed = failed || !d.OK && strings.Contains(d.Detail, "could not start")
			case event.Error:
				var d event.ErrorData
				_ = ev.Decode(&d)
				errored = errored || strings.Contains(d.Text, "could not start")
			}
		}
		if !failed || !errored {
			t.Errorf("undelivered recorded %v, error notice %v", failed, errored)
		}
		if r, why := rt.routeUserMessage(event.UserMessageData{Text: "hello"}); r != nil {
			t.Errorf("a message would go to the dead relay (%s)", why)
		}
	})
}

// relay_control switch stops the old agent and launches the new one off
// the loop goroutine. The old relay ends on the record right before the
// new one starts; the new one gets the handoff brief, then what the user
// sent during the handover.
func TestRelaySwitchRunsOffTheLoop(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("one")}, newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	args := map[string]any{"action": "switch", "profile": "claude"}
	out := make(chan string, 1)
	go func() {
		text, _ := rt.relayTool("relay_control", argStr(args), argNum(args), argList(args), false)
		out <- text
	}()
	g.waitEntered(t)
	loopResponds(t, rt)
	userSays(t, rt, "use the staging database")
	if len(old.sentTexts()) != 0 {
		t.Fatalf("typed into the stopped agent: %q", old.sentTexts())
	}
	g.release <- struct{}{}
	var text string
	select {
	case text = <-out:
	case <-time.After(5 * time.Second):
		t.Fatal("switch never returned")
	}
	if !strings.Contains(text, "Handed over") {
		t.Fatalf("switch: %q", text)
	}
	sent := next.sentTexts()
	if len(sent) != 2 || sent[1] != "use the staging database" {
		t.Fatalf("new agent got %q", sent)
	}
	rt.sync(func() {
		var order []string
		for _, ev := range rt.st.Events {
			switch ev.Type {
			case event.RelayEnd, event.RelayStart:
				var d struct {
					Name string `json:"name"`
				}
				_ = ev.Decode(&d)
				order = append(order, string(ev.Type)+":"+d.Name)
			}
		}
		want := []string{string(event.RelayStart) + ":codex", string(event.RelayEnd) + ":codex", string(event.RelayStart) + ":claude"}
		if !slices.Equal(order, want) {
			t.Errorf("recorded %v, want %v", order, want)
		}
	})
}

// The reviewer's scenario: a model change starts Codex again; while that
// launch is under way the user picks the default again. That is not "what
// it already runs": the launch starts the first pick, so the second waits
// and is applied once the new agent is up (at once, review 15 finding 9:
// nothing is typed into an agent that is to be replaced).
func TestRelayRepickDuringLaunchBackToDefault(t *testing.T) {
	first, second := newFakeSeat("two"), newFakeSeat("three")
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), first, second}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	var msg string
	rt.sync(func() {
		var err error
		if msg, err = rt.repickRelay("codex", relayPick{}); err != nil {
			t.Errorf("repick back: %v", err)
		}
		if got := rt.relayRoutes()["codex"]; !got.Waiting || got.Model != "" {
			t.Errorf("while starting, the default pick should wait: %+v", got)
		}
	})
	if strings.Contains(msg, "already runs") {
		t.Fatalf("repick back to the default during the launch: %q", msg)
	}
	g.release <- struct{}{}
	// Up: the waiting change applies.
	g.waitEntered(t)
	rt.sync(func() {
		rel := rt.st.Relays["codex"]
		if rel.Model != "gpt-6.2-astra" {
			t.Errorf("the launch under way started %q", rel.Model)
		}
		if p, ok, waiting := rt.nextPick("codex"); !ok || !waiting || p != (relayPick{}) {
			t.Errorf("the default pick is not waiting after the launch: %v %v", p, ok)
		}
	})
	if !first.wasKilled() {
		t.Error("the agent the first change brought up was not replaced")
	}
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	rt.sync(func() {
		rel := rt.st.Relays["codex"]
		if _, ok, _ := rt.nextPick("codex"); rel.Model != "" || ok {
			t.Errorf("after the waiting change: model %q, waiting %v", rel.Model, rt.relayNext)
		}
	})
	g.mu.Lock()
	defer g.mu.Unlock()
	if n := len(g.specs); n != 3 {
		t.Fatalf("%d launches, want 3", n)
	}
}

// Picking again what the launch under way already starts changes nothing
// and starts nothing more.
func TestRelayRepickDuringLaunchSamePick(t *testing.T) {
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		_, _ = rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""})
	})
	g.waitEntered(t)
	rt.sync(func() {
		_, _ = rt.repickRelay("codex", relayPick{"gpt-5", ""})
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	rt.sync(func() {
		if _, ok, _ := rt.nextPick("codex"); rt.st.Relays["codex"].Model != "gpt-6.2-astra" || ok {
			t.Errorf("after the launch: model %q, waiting %v", rt.st.Relays["codex"].Model, rt.relayNext)
		}
	})
}
