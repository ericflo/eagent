package harness

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/relay"
)

// sr11HeldForRestart restarts Codex (gated launch), waits for the launch to
// begin, and sends a message that is held for the restart. Returns its id.
func sr11HeldForRestart(t *testing.T) (*Runtime, *gatedLaunches, string) {
	t.Helper()
	one := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{one, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	relayToolAsync(rt, map[string]any{"action": "restart"})
	one.waitGone(t)
	one.end()
	g.waitEntered(t) // the restart is launching
	args := map[string]any{"relay": "codex", "text": "also check the docs"}
	text, _ := rt.relayTool("relay_send", argStr(args), argNum(args), argList(args), false)
	if !strings.Contains(text, "typed in once it is up") {
		t.Fatalf("setup: relay_send not held for the restart: %q", text)
	}
	return rt, g, lastSendID(rt)
}

// SR11-A. Codex is being restarted; a message is held for the restart;
// then Codex is stopped (the stop cuts the start: stopLaunches), and eagent
// is cut off before the start ends. The message was never typed. Uncut,
// finishLaunch records it not_sent ("the agent could not start again").
// At the resume, settleEnded only treats as held what was recorded after
// the stop's relay.end; this one was recorded before it, so it is settled
// as typed into the stopped agent: unconfirmed.
func TestSR11RelayResumeCallsASendHeldForARestartThenStoppedUnconfirmed(t *testing.T) {
	rt, g, id := sr11HeldForRestart(t)
	if text := recv(t, relayToolAsync(rt, map[string]any{"action": "stop"}), "stop"); !strings.Contains(text, "Stopped") {
		t.Fatalf("setup: stop: %q", text)
	}
	path, project := rt.sess.Path, rt.opts.Project
	rt.sess.Close() // cut off here

	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt2.sess.Close() })
	if o := rt2.st.SendOutcome(id); o != "not_sent" {
		t.Errorf("%s, held for a restart the stop cut, never typed: after the resume %q; want not_sent", id, o)
	}
	g.release <- struct{}{}
}

// SR11-B. As SR11-A without the stop: eagent is cut off while the restart
// is launching. At the resume the old agent is gone (the restart killed
// it), so reattachRelays settles the relay as exited: the held message,
// never typed, is recorded unconfirmed.
func TestSR11RelayResumeCallsASendHeldForACutRestartOfARunningRelayUnconfirmed(t *testing.T) {
	rt, g, id := sr11HeldForRestart(t)
	path, project := rt.sess.Path, rt.opts.Project
	rt.sess.Close() // cut off here

	saved := attachRelay
	attachRelay = func(relay.Spec, string, string, string, string, time.Time) (relaySeat, error) {
		return nil, errors.New("no such terminal")
	}
	t.Cleanup(func() { attachRelay = saved })
	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt2.sess.Close() })
	if o := rt2.st.SendOutcome(id); o != "not_sent" {
		t.Errorf("%s, held for a restart that never came up, never typed: after the resume %q; want not_sent", id, o)
	}
	g.release <- struct{}{}
}
