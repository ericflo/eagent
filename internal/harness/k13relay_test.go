package harness

import (
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// SR10-3. A switch hands Codex's work to Claude while the user's message
// typed into Codex is not yet confirmed: the switch settles only eagent's
// own words to Codex (finishLaunch: the brief quotes the user's), so the
// user's message stays waiting on the ended Codex record. On a resume, the
// new reattachRelays settles every ended relay with sends waiting, and
// records that message "could not be confirmed", which wakes eagent with
// "send it again", though Claude's brief carried it.
func TestSR10RelayResumeAfterASwitchCallsTheBriefedUserMessageUnconfirmed(t *testing.T) {
	one := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{one, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	_, id := k2Forward(t, rt, one, "run the tests", "queue")
	switched := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	one.waitGone(t)
	one.end()
	g.waitEntered(t)
	g.release <- struct{}{}
	text := recv(t, switched, "switch")
	if !strings.Contains(text, "Handed over") {
		t.Fatalf("setup: switch: %q", text)
	}
	k1QueuesIdle(t, rt)
	before := k2Outcome(rt, id)
	var brief bool
	rt.sync(func() {
		for _, ev := range rt.st.Events {
			var d event.RelaySendData
			if ev.Type == event.RelaySend && ev.Decode(&d) == nil && d.Name != "codex" && strings.Contains(d.Text, "run the tests") {
				brief = true
			}
		}
	})
	if !brief || before != "waiting" {
		t.Fatalf("setup: brief quotes the message %v, outcome before the resume %q", brief, before)
	}
	path, project := rt.sess.Path, rt.opts.Project
	rt.sess.Close()

	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt2.sess.Close() })
	if o := rt2.st.SendOutcome(id); o != before {
		t.Errorf("%s, the user's message the switch's brief carried: after a resume its outcome is %q (eagent is told to send it again); want it left as the switch left it", id, o)
	}
}

// SR10-4. Codex is stopped (its stop has settled); Codex is restarted and,
// while the restart is under way, a message is sent to it and held for the
// restart. eagent is cut off before the restart is recorded. The message
// was never typed into anything; the resume's new settle of ended relays
// records it "the agent was stopped before its arrival showed, so whether
// it arrived cannot be confirmed". It did not go: not_sent.
func TestSR10RelayResumeCallsASendHeldForACutRestartUnconfirmed(t *testing.T) {
	one := newK2FeedSeat(t)
	g := useGatedLaunches(t, []relaySeat{one, newFakeSeat("two")}, nil)
	rt := newLaunchRuntime(t)
	stopped := relayToolAsync(rt, map[string]any{"action": "stop"})
	one.waitGone(t)
	one.end()
	recv(t, stopped, "relay_control stop")
	relayToolAsync(rt, map[string]any{"action": "restart"})
	g.waitEntered(t) // the restart is launching
	args := map[string]any{"relay": "codex", "text": "also check the docs"}
	text, _ := rt.relayTool("relay_send", argStr(args), argNum(args), argList(args), false)
	if !strings.Contains(text, "typed in once it is up") {
		t.Fatalf("setup: relay_send not held for the restart: %q", text)
	}
	id := lastSendID(rt)
	path, project := rt.sess.Path, rt.opts.Project
	rt.sess.Close() // cut off here

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
