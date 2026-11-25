package harness

import (
	"context"
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
	close(rt.closed) // the loop has stopped; nothing serves r.sync
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
			case <-time.After(500 * time.Millisecond):
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
		if forwardedAs(st, "claude", seqA) == "" {
			t.Error("relay_forward could send A to the new agent again")
		}
	})
	text, isErr := rt.relayTool("relay_forward", argStr(map[string]any{"relay": "claude", "seq": seqA}), argNum(map[string]any{"relay": "claude", "seq": seqA}), argList(nil), false)
	if !isErr || !strings.Contains(text, "already went") {
		t.Fatalf("relay_forward of A to the new agent: %q", text)
	}
}
