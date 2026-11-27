package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// k3ClaudeRuntime is newLaunchRuntime with Claude, whose after-turn
// messages wait in a spool for its Stop hook.
func k3ClaudeRuntime(t *testing.T) *Runtime {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "claude"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	relayServeLoop(t, rt)
	rt.sync(func() { rt.st.Relays["claude"].State = "idle" })
	return rt
}

// A6, its restart half. A message is held for the end of Claude's turn and
// still waits in the spool, unclaimed, when Claude is started again: no
// hook took it, and the new launch clears the spool. It certainly did not
// go: it is not sent, once (not "unconfirmed"), and may be sent again.
func TestK3AHeldMessageStillInTheSpoolAtARestartIsNotSent(t *testing.T) {
	seat := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{seat, newFakeSeat("two")}, nil)
	rt := k3ClaudeRuntime(t)
	var seq int64
	rt.sync(func() {
		d := event.UserMessageData{Text: "then run the linter", Delivery: "after_turn"}
		ev := rt.append(event.New(event.UserMessage, event.ActorUser, d))
		seq = ev.Seq
		rel, why := rt.routeUserMessage(d)
		if rel == nil {
			t.Errorf("not routed: %s", why)
			return
		}
		rt.forwardUserMessage(rel, ev.Seq, d)
	})
	waitFor(t, "the message handed over", func() bool { return len(seat.sentTexts()) == 1 })
	k1QueuesIdle(t, rt)
	id := lastSendID(rt)
	// Where the seat put it (a fake seat writes no spool): unclaimed.
	spool := filepath.Join(rt.sess.Path, "relays", "claude", "after_turn")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(spool, fmt.Sprintf("%d-%s.txt", time.Now().UnixNano(), id))
	if err := os.WriteFile(file, []byte("then run the linter"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt.sync(func() {
		if err := rt.restartRelayWith(rt.st.Relays["claude"], relayPick{"opus", ""}); err != nil {
			t.Errorf("restart: %v", err)
		}
	})
	seat.waitGone(t)
	seat.end()
	g.release <- struct{}{}
	waitRelayStarts(t, rt)
	if got := deliveredFor(rt, id); len(got) != 1 || got[0].Outcome != event.DeliveryNotSent {
		t.Fatalf("%s: %+v; want its one outcome, not_sent", id, got)
	}
	var prev, why string
	rt.sync(func() { prev, why = forwardedAs(rt.st, "claude", seq, "") })
	if prev != "" {
		t.Errorf("forwarding the message again is refused as %s: %q", prev, why)
	}
}
