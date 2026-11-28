package harness

import (
	"context"
	"errors"
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

func (c *closingSeat) Send(id, text, mode string) (_ string, err error) {
	defer c.sendStart(id)(&err)
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
		// The restart a model change makes (a message on its way now holds
		// a change asked back, item 3; this test is about the restart).
		if err := rt.restartRelayWith(rt.st.Relays["codex"], relayPick{"gpt-6.2-astra", ""}); err != nil {
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
		return failed && strings.Contains(d.Detail, "stopped or restarted before the message was typed")
	})
}

// Once the session has ended the loop runs nothing, so a tool call that
// arrives then (a late turn, a slash command's goroutine) learns nothing
// about the relay. It says the session is ending; it never panics.
func TestRelayToolsSayTheSessionIsEndingOnceItHasEnded(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	useGatedLaunches(t, []relaySeat{newFakeSeat("one")}, nil)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	// The loop has stopped, and finish has ended its queue: nothing serves
	// r.sync (enqueue refuses).
	close(rt.closed)
	rt.queueMu.Lock()
	rt.queueEnded = true
	rt.queueMu.Unlock()
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"relay_send", map[string]any{"text": "hello"}},
		{"relay_forward", map[string]any{"seq": 1}},
		{"relay_screen", map[string]any{}},
		{"relay_status", map[string]any{}},
		{"relay_autopilot", map[string]any{}},
		{"relay_control", map[string]any{"action": "interrupt"}},
		{"relay_control", map[string]any{"action": "stop"}},
		{"relay_control", map[string]any{"action": "restart"}},
		{"relay_control", map[string]any{"action": "model", "model": "gpt-6.2-astra"}},
		{"relay_control", map[string]any{"action": "switch", "profile": "claude"}},
		{"relay_start", map[string]any{"profile": "claude"}},
	} {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("%s %v panicked: %v", c.tool, c.args, p)
				}
			}()
			text, isErr := rt.relayTool(c.tool, argStr(c.args), argNum(c.args), argList(c.args), false)
			if !isErr || !strings.Contains(text, "ending") {
				t.Errorf("%s %v: %q (error %v), want that the session is ending", c.tool, c.args, text, isErr)
			}
		}()
	}
}

// Stop session while a restart started from a slash command (/restart) or
// a model change is launching: the process does not end before the agent
// that start brings up is stopped, so no agent is left running that the
// record does not know about.
func TestStopSessionWaitsForARestartStartedOffTheTurn(t *testing.T) {
	for _, how := range []string{"/restart", "model change"} {
		t.Run(how, func(t *testing.T) {
			first, next := newFakeSeat("one"), newFakeSeat("two")
			g := useGatedLaunches(t, []relaySeat{first, next}, nil)
			t.Setenv("EAGENT_TEST_KEY", "x")
			relayAgentHomes(t)
			ui := &fakeUI{input: make(chan string, 16)}
			rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex", Interactive: true}, ui)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			done := make(chan int, 1)
			go func() { done <- rt.Run(ctx) }()
			rt.sync(func() {
				rt.st.Relays["codex"].State = "idle"
				if how == "/restart" {
					rt.relayCommand([]string{"/restart"}, nil)
				} else if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
					t.Errorf("repick: %v", err)
				}
			})
			g.waitEntered(t)
			rt.sync(func() { rt.stopSession("stopped from the app") })
			select {
			case <-done:
				t.Fatal("the session ended while an agent was still starting")
			case <-time.After(200 * time.Millisecond): // a stop that did not wait ends in a few ms
			}
			g.release <- struct{}{}
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("the session did not end")
			}
			if !next.wasKilled() {
				t.Fatal("the session ended and left the agent the restart brought up running")
			}
		})
	}
}

// A message the user sent while a switch was under way goes to the new
// agent, and the record says so: it is the new agent's send (who sent
// last, what the user sent it, what a later handoff brief carries, what
// relay_forward may not send again), its arrival is confirmed under the
// new name, and the old relay does not keep it as unconfirmed.
func TestMessageHeldDuringASwitchIsTheNewAgentsOwn(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("one")}, newFakeSeat("Done: switched the database.")
	next.waiting = true // each reply is a turn that ran nothing
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	out := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	userSays(t, rt, "A: use the staging database")
	var seqA int64
	rt.sync(func() { seqA = rt.st.LastUserSeq })
	g.release <- struct{}{}
	recv(t, out, "switch")
	waitFor(t, "two turns from the new agent", func() bool {
		n := 0
		rt.sync(func() {
			if rel := rt.st.Relays["claude"]; rel != nil {
				n = rel.Turns
			}
		})
		return n == 2
	})
	rt.sync(func() {
		st := rt.st
		claude, codex := st.Relays["claude"], st.Relays["codex"]
		if claude.LastSendFrom != "user" {
			t.Errorf("the new agent's last send is from %q, want the user", claude.LastSendFrom)
		}
		var turns []event.RelayTurnData
		var moved []event.RelayMovedData
		for _, ev := range st.Events {
			switch ev.Type {
			case event.RelayTurn:
				var d event.RelayTurnData
				_ = ev.Decode(&d)
				turns = append(turns, d)
			case event.RelayMoved:
				var d event.RelayMovedData
				_ = ev.Decode(&d)
				moved = append(moved, d)
			}
		}
		if len(turns) != 2 || turns[1].Idle {
			t.Errorf("turn idle marks %+v: the reply to A answered the user, not eagent", turns)
		}
		if len(moved) != 1 || moved[0].Name != "claude" || moved[0].Prev != "codex" || moved[0].From != "user" || moved[0].Seq != seqA {
			t.Errorf("moved: %+v", moved)
		}
		if sent := strings.Join(userSends(st, "claude", 8), "\n"); !strings.Contains(sent, "A: use the staging") || !strings.Contains(sent, "arrived") {
			t.Errorf("what the user sent the new agent: %q", sent)
		}
		if sent := strings.Join(userSends(st, "codex", 8), "\n"); !strings.Contains(sent, "went to Claude") {
			t.Errorf("what the user sent the old agent: %q", sent)
		}
		if len(codex.Undelivered) != 0 || len(claude.Undelivered) != 0 {
			t.Errorf("unconfirmed: codex %v, claude %v", codex.Undelivered, claude.Undelivered)
		}
		if brief := rt.handoffBrief(claude); !strings.Contains(brief, "A: use the staging") {
			t.Errorf("a later handoff brief from the new agent leaves out A:\n%s", brief)
		}
		if id, _ := forwardedAs(st, "claude", seqA, ""); id == "" {
			t.Error("relay_forward could send A to the new agent again")
		}
	})
	text, isErr := rt.relayTool("relay_forward", argStr(map[string]any{"relay": "claude", "seq": seqA}), argNum(map[string]any{"relay": "claude", "seq": seqA}), argList(nil), false)
	if !isErr || !strings.Contains(text, "already went") {
		t.Fatalf("relay_forward of A to the new agent: %q", text)
	}
}

// A handoff brief that could not be typed is on the record as not
// delivered, like any other send that fails, so eagent is woken and the
// new relay does not wait for it for ever.
func TestFailedHandoffBriefIsRecordedUndelivered(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("one")}, newFakeSeat("two")
	next.sendErr = errors.New("the pane is gone")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	out := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	if text := recv(t, out, "switch"); !strings.Contains(text, "could not be typed") {
		t.Fatalf("switch: %q", text)
	}
	brief := lastSendID(rt)
	waitFor(t, "the brief recorded undelivered", func() bool {
		d, failed := failedDeliveries(rt)[brief]
		return failed && d.Name == "claude" && strings.Contains(d.Detail, "pane is gone")
	})
	rt.sync(func() {
		if n := len(rt.st.Relays["claude"].Undelivered); n != 0 {
			t.Errorf("the new relay still waits for %d send(s)", n)
		}
	})
}

// slowKillSeat is a fake agent whose terminal takes a while to close.
type slowKillSeat struct {
	*fakeSeat
	killing chan struct{}
	finish  chan struct{}
}

func (s *slowKillSeat) Kill() error {
	close(s.killing)
	<-s.finish
	return s.fakeSeat.Kill()
}

// A stop whose kill is slow, and a restart asked for meanwhile: the stop is
// on the record before the restart begins, and the restart waits for the
// old terminal to be gone, so it is the restarted agent that runs and the
// record says so (it used to end up stopped on the record while it ran).
func TestStopThenRestartLeavesTheRestartedAgentRunning(t *testing.T) {
	old := &slowKillSeat{fakeSeat: newFakeSeat("one"), killing: make(chan struct{}), finish: make(chan struct{})}
	next := newFakeSeat("two")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	stopped := relayToolAsync(rt, map[string]any{"action": "stop"})
	<-old.killing
	restarted := relayToolAsync(rt, map[string]any{"action": "restart", "relay": "codex"})
	time.Sleep(100 * time.Millisecond)
	close(old.finish)
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, stopped, "stop")
	if text := recv(t, restarted, "restart"); !strings.Contains(text, "Restarted") {
		t.Fatalf("restart: %q", text)
	}
	rt.sync(func() {
		rel := rt.st.Relays["codex"]
		if !rel.Active() || rt.relays.get("codex") != relaySeat(next) {
			t.Errorf("after stop then restart: active %v, watched %v", rel.Active(), rt.relays.get("codex") != nil)
		}
		last := ""
		for _, ev := range rt.st.Events {
			if ev.Type == event.RelayStart || ev.Type == event.RelayEnd {
				last = ev.Type
			}
		}
		if last != event.RelayStart {
			t.Errorf("the record ends with %s", last)
		}
	})
}

// An upgrade takes minutes. If the agent is stopped, or handed over to
// another, while it runs, it is not brought back afterwards.
func TestUpgradeDoesNotBringBackAnAgentStoppedMeanwhile(t *testing.T) {
	for _, how := range []string{"stop", "switch"} {
		t.Run(how, func(t *testing.T) {
			g := useGatedLaunches(t, []relaySeat{&lingerSeat{newFakeSeat("one")}, newFakeSeat("two"), newFakeSeat("three")}, nil)
			upgrading, finish := make(chan struct{}), make(chan struct{})
			oldUpgrade := upgradeAgent
			upgradeAgent = func(string) (string, error) {
				close(upgrading)
				<-finish
				return "codex 9.9.9", nil
			}
			t.Cleanup(func() { upgradeAgent = oldUpgrade })
			rt := newLaunchRuntime(t)
			upgraded := relayToolAsync(rt, map[string]any{"action": "upgrade"})
			<-upgrading
			if how == "stop" {
				recv(t, relayToolAsync(rt, map[string]any{"action": "stop", "relay": "codex"}), "stop")
			} else {
				out := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude", "relay": "codex"})
				g.waitEntered(t)
				g.release <- struct{}{}
				recv(t, out, "switch")
			}
			close(finish)
			text := recv(t, upgraded, "upgrade")
			if !strings.Contains(text, "stays stopped") {
				t.Errorf("upgrade: %q", text)
			}
			rt.sync(func() {
				if rel := rt.st.Relays["codex"]; rel.Active() {
					t.Error("the upgrade brought the stopped agent back")
				}
				if n := len(rt.st.ActiveRelays()); n > 1 {
					t.Errorf("%d agents run", n)
				}
			})
			g.mu.Lock()
			launches := len(g.specs)
			g.mu.Unlock()
			if want := map[string]int{"stop": 1, "switch": 2}[how]; launches != want {
				t.Errorf("%d launches, want %d", launches, want)
			}
		})
	}
}

// "/model default" goes back to the agent's own model and keeps the effort
// picked (TestModelCommand); its help says so, and says how to go back to
// both: "/model default default".
func TestModelDefaultCommandGoesBackToBothDefaults(t *testing.T) {
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		rel := rt.st.Relays["codex"]
		rel.Model, rel.Effort = "gpt-6.2-astra", "high"
		rt.relayCommand([]string{"/model", "default", "default"}, nil)
	})
	g.waitEntered(t)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	waitFor(t, "the restart recorded", func() bool {
		n := 0
		rt.sync(func() {
			for _, ev := range rt.st.Events {
				if ev.Type == event.RelayStart {
					n++
				}
			}
		})
		return n == 2
	})
	rt.sync(func() {
		if rel := rt.st.Relays["codex"]; rel.Model != "" || rel.Effort != "" {
			t.Errorf("after /model default default: model %q, effort %q", rel.Model, rel.Effort)
		}
	})
	ui := rt.ui.(*fakeUI)
	rt.sync(func() { rt.relayCommand([]string{"/model"}, nil) })
	waitFor(t, "the help", func() bool {
		said := strings.Join(ui.said(), "|")
		return strings.Contains(said, "/model default goes back to its own model and keeps the effort") && strings.Contains(said, "/model default default")
	})
}
