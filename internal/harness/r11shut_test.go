package harness

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// r11PhoneSession runs a phone session at a terminal whose agent (codex)
// is seat; the phone's user messages go to it.
func r11PhoneSession(t *testing.T, seat relaySeat) (*Runtime, *relayPhone, <-chan int) {
	t.Helper()
	useGatedLaunches(t, []relaySeat{seat}, nil)
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
	fp := newRelayPhone()
	t.Cleanup(fp.srv.Close)
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	project := t.TempDir()
	cfg := relayPhoneConfig(t, project, model.srv.URL, fp)
	rt, err := New(cfg, Options{Project: project, Relay: "codex", Interactive: true}, &fakeUI{input: make(chan string, 4)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	rt.sync(func() { rt.st.Relays["codex"].State = "idle" })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	return rt, fp, done
}

// r11Receipt is the last receipt the phone was given for its message id.
func r11Receipt(fp *relayPhone, id string) string {
	got := ""
	for _, m := range fp.metaPatches() {
		if rr, ok := m["relay_receipts"].(map[string]any); ok {
			if s, ok := rr[id].(string); ok {
				got = s
			}
		}
	}
	return got
}

// Finding 6. Two phone messages for the agent: the first is being typed
// when the session quits, the second waits behind it. The second was never
// typed; the record says not_sent, and the phone's receipt for it says
// failed (a Retry), not the "sent" it showed while it waited.
func TestR11AQueuedPhoneSendNotSentAtTheEndFailsOnThePhone(t *testing.T) {
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	rt, fp, done := r11PhoneSession(t, seat)
	fp.userReply("check the version")
	select {
	case <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first send was never typed")
	}
	typing := lastSendID(rt)
	fp.userReply("then run the tests")
	second := fp.lastMessageID()
	waitFor(t, "the second send queued", func() bool { return lastSendID(rt) != typing })
	queued := lastSendID(rt)
	waitFor(t, "the second message's receipt", func() bool { return r11Receipt(fp, second) != "" })
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never closed")
	}
	seat.result <- nil
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	if o := state.Replay(evs).SendOutcome(queued); o != state.SendNotSent {
		t.Fatalf("the queued send reads %q on the record, want not_sent", o)
	}
	if got := r11Receipt(fp, second); got != "failed" {
		t.Errorf("the phone's receipt for the send recorded not_sent is %q, want failed", got)
	}
}

// r11Unanswerable is a batch session that stopped on a question nobody
// could answer, its agent still running.
func r11Unanswerable(t *testing.T) (string, string) {
	t.Helper()
	rt, _, _ := batchExit(t, 2, brief, "Both are ready. Merge to main, or open a pull request?", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge to main, or open a pull request?"}`)}}
		}
		return hold()
	})
	return rt.sess.Path, rt.opts.Project
}

// Finding 4, a question nobody can answer. The session is resumed with
// /restart or /switch: the command runs (it is not refused as the session
// ending), its outcome is told, and then the session ends again on the
// open question.
func TestR11ACommandResumingAnUnanswerableSessionRuns(t *testing.T) {
	for _, c := range []struct{ cmd, told string }{{"/restart", "Restarted"}, {"/switch claude", "Handed over"}} {
		t.Run(c.cmd, func(t *testing.T) {
			path, project := r11Unanswerable(t)
			useRelayFactory(t, "Back on it.") // a seat of its own for each agent
			model := newScripted(func(string, []map[string]any) reply { return hold() })
			t.Cleanup(model.srv.Close)
			ui2 := &fakeUI{}
			rt2, err := Resume(testConfig(model.srv.URL), Options{Project: project, Prompt: c.cmd}, ui2, path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := contextWithCancel()
			defer cancel()
			done := make(chan int, 1)
			go func() { done <- rt2.Run(ctx) }()
			var code int
			select {
			case code = <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("the session did not end")
			}
			if saidWith(ui2, "the session is ending") || !saidWith(ui2, c.told) {
				t.Fatalf("%s was refused or not told: said %q", c.cmd, ui2.said())
			}
			if code != 2 {
				t.Fatalf("exit code %d, want 2 (the question is still open)", code)
			}
		})
	}
}

// Finding 4, a finished session. Resumed with /restart, it asks for no
// closing look while the restart is under way: the agent the restart
// brings up keeps it going, and nobody is told the session is ending.
func TestR11ARestartResumingAFinishedSessionAsksNoClosingLook(t *testing.T) {
	path, project := rv10Finished(t)
	seat2 := newFakeSeat("Back on it.")
	entered, gate := make(chan struct{}, 1), make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	old := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		entered <- struct{}{}
		<-gate
		return seat2, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	t.Cleanup(func() { release(); launchRelay = old })
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	ui2 := &fakeUI{}
	rt2, err := Resume(testConfig(model.srv.URL), Options{Project: project, Prompt: "/restart"}, ui2, path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := contextWithCancel()
	done := make(chan int, 1)
	go func() { done <- rt2.Run(ctx) }()
	t.Cleanup(func() {
		release()
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the start never began")
	}
	closing := func() bool {
		asked := true
		rt2.sync(func() { asked = rt2.turns.finalAsked })
		return asked
	}
	if closing() {
		t.Fatal("a closing look was asked for while the restart was under way")
	}
	release()
	waitFor(t, "the restart's outcome", func() bool { return saidWith(ui2, "Restarted") || len(done) > 0 })
	if !saidWith(ui2, "Restarted") || closing() || len(done) > 0 {
		t.Fatalf("said %q, exited %v", ui2.said(), len(done) > 0)
	}
}
