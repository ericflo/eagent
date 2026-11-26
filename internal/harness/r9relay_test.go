package harness

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

const r9Text = "also sweep the floor"

// r9Restarted restarts codex for a model change, has the user write while
// the restart is under way, and lets it finish onto an agent that types
// what it is sent and confirms nothing. It returns the runtime, that agent
// and the held send's id.
func r9Restarted(t *testing.T) (*Runtime, *quietSeat, string) {
	t.Helper()
	next := &quietSeat{fakeSeat: newFakeSeat("two")}
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), next}, nil)
	rt := newLaunchRuntime(t)
	rt.sync(func() {
		if _, err := rt.repickRelay("codex", relayPick{"gpt-6.2-astra", ""}); err != nil {
			t.Errorf("repick: %v", err)
		}
	})
	g.waitEntered(t)
	userSays(t, rt, r9Text)
	id := lastSendID(rt)
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	waitFor(t, "the held message typed into the new agent", func() bool { return slices.Contains(next.typedTexts(), r9Text) })
	return rt, next, id
}

// r9ExitSettles has the agent exit and checks the send typed into it, and
// never confirmed, gets its outcome once.
func r9ExitSettles(t *testing.T, rt *Runtime, seat *quietSeat, id string) {
	t.Helper()
	rt.sync(func() {
		if n := rt.st.Relays["codex"].Awaited(); n != 1 {
			t.Errorf("%d sends awaited from the new agent, want 1 (%s)", n, id)
		}
	})
	seat.emit(relay.Event{Kind: relay.KindState, State: "exited", Detail: "pane gone"})
	waitFor(t, "the exit on the record", func() bool {
		var st string
		rt.sync(func() { st = rt.st.Relays["codex"].State })
		return st == "exited"
	})
	rt.sync(func() {
		n := 0
		for _, ev := range rt.st.Events {
			var d event.RelayDeliveredData
			if ev.Type == event.RelayDelivered && ev.Decode(&d) == nil && d.ID == id {
				n++
			}
		}
		if o := rt.st.SendOutcome(id); n != 1 || o != state.SendUnconfirmed {
			t.Errorf("after the exit, %s has %d outcomes on the record and reads %q", id, n, o)
		}
	})
}

// (a) The restarted agent exits before confirming the send held for its
// start: the send gets its outcome, and is not left waiting for ever.
func TestR9ASendHeldForARestartIsSettledWhenTheAgentExits(t *testing.T) {
	rt, seat, id := r9Restarted(t)
	r9ExitSettles(t, rt, seat, id)
}

// (b) eagent stops and is resumed before the send held for the restart
// is confirmed: the reattached agent is given it to confirm.
func TestR9ASendHeldForARestartIsHandedToTheReattachedAgent(t *testing.T) {
	rt, _, id := r9Restarted(t)
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	project, path := cutWrite(t, filepath.Base(rt.sess.Path), evs)
	specs := captureAttach(t, nil)
	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	rt2.sess.Close()
	got := specs()
	if len(got) != 1 {
		t.Fatalf("%d reattaches", len(got))
	}
	if r := got[0].Restore; len(r) != 1 || r[0].ID != id || r[0].Text != r9Text {
		t.Fatalf("the reattached agent was given %+v, want %s (%q)", r, id, r9Text)
	}
}

// A new agent started under the name of one that was stopped, with a
// send for that name held for its start: the send is the new agent's to
// confirm, and gets its outcome when that agent exits.
func TestR9ASendHeldForANewAgentUnderAStoppedNameIsSettledWhenItExits(t *testing.T) {
	next := &quietSeat{fakeSeat: newFakeSeat("two")}
	g := useGatedLaunches(t, []relaySeat{newFakeSeat("one"), next}, nil)
	rt := newLaunchRuntime(t)
	tool := func(name string, args map[string]any) (string, bool) {
		return rt.relayTool(name, argStr(args), argNum(args), argList(args), false)
	}
	if text, isErr := tool("relay_control", map[string]any{"relay": "codex", "action": "stop"}); isErr {
		t.Fatalf("stop: %s", text)
	}
	started := make(chan string, 1)
	go func() {
		text, _ := tool("relay_start", map[string]any{"profile": "codex"})
		started <- text
	}()
	g.waitEntered(t)
	if text, isErr := tool("relay_send", map[string]any{"relay": "codex", "text": r9Text}); isErr || !strings.Contains(text, "is starting") {
		t.Fatalf("send while the new agent starts: %q", text)
	}
	id := lastSendID(rt)
	g.release <- struct{}{}
	select {
	case text := <-started:
		if !strings.Contains(text, "Started.") {
			t.Fatalf("relay_start: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay_start never returned")
	}
	waitFor(t, "the held message typed into the new agent", func() bool { return slices.Contains(next.typedTexts(), r9Text) })
	r9ExitSettles(t, rt, next, id)
}
