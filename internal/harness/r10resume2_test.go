package harness

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// rv10Finished is a batch session whose agent was stopped and the user told.
func rv10Finished(t *testing.T) (string, string) {
	t.Helper()
	rt, _, _ := batch(t, brief+" Stop when done.", "Done: #42 and #44 are merged.", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
			return stopsAndReports("Done: Codex merged #42 and #44 on main.")
		}
		return hold()
	})
	return rt.sess.Path, rt.opts.Project
}

func rv10Logged(ui *fakeUI, part string) bool {
	for _, l := range ui.logged() {
		if strings.Contains(l, part) {
			return true
		}
	}
	return false
}

// A finished batch session resumed with /upgrade: the upgrade takes
// minutes. The session does not end under it (at 59a2069 it did, and
// nobody was told): it waits until the upgrade is done and told.
func TestRV10UpgradeOnResumedFinishedSession(t *testing.T) {
	path, project := rv10Finished(t)
	useRelayFactory(t, "Back on it.")
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	old := upgradeAgent
	entered := make(chan struct{}, 1)
	upgradeAgent = func(string) (string, error) { entered <- struct{}{}; <-gate; return "upgraded", nil }
	t.Cleanup(func() { release(); upgradeAgent = old })

	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	ui2 := &fakeUI{}
	rt2, err := Resume(testConfig(model.srv.URL), Options{Project: project, Prompt: "/upgrade"}, ui2, path)
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
		t.Fatal("the upgrade never began")
	}
	var ending, asked bool
	rt2.sync(func() { ending, asked = rt2.ending, rt2.turns.finalAsked })
	if ending || asked || len(done) > 0 {
		t.Fatalf("while the upgrade runs: ending %v, closing look asked %v, ended %v", ending, asked, len(done) > 0)
	}
	release()
	waitFor(t, "the upgrade's outcome", func() bool { return saidWith(ui2, "Upgraded") || len(done) > 0 })
	if !saidWith(ui2, "Upgraded") || len(done) > 0 {
		t.Fatalf("said %q, ended %v", ui2.said(), len(done) > 0)
	}
}

// A finished batch session resumed with /restart whose start fails: the
// failure is told before session.end.
func TestRV10FailedRestartOnResumedSessionIsTold(t *testing.T) {
	path, project := rv10Finished(t)
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	old := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		<-gate
		return nil, relay.Started{}, errors.New("codex is not installed (not on PATH)")
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
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- rt2.Run(ctx) }()
	// Let the closing look come and go while the start is under way.
	time.Sleep(500 * time.Millisecond)
	if len(done) > 0 {
		t.Fatal("the session ended under the start")
	}
	relAt := time.Now()
	release()
	var code int
	select {
	case code = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	t.Logf("ended %v after the start failed, code %d; said %q; logs %q", time.Since(relAt), code, ui2.said(), ui2.logged())
	evs, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	told, endAt := -1, -1
	for i, ev := range evs {
		var d event.MessageData
		if ev.Type == event.Message && ev.Decode(&d) == nil && strings.Contains(d.Text, "did not work") {
			told = i
		}
		if ev.Type == event.SessionEnd {
			endAt = i
		}
	}
	if told < 0 || told > endAt {
		t.Fatalf("the failed restart told at %d, session end at %d", told, endAt)
	}
}

// The session is interrupted while a command's work is under way (an
// upgrade, which takes minutes; no closing look is asked for meanwhile).
// The end does not wait for it.
func TestRV10InterruptDuringSlowCommandEndsAtOnce(t *testing.T) {
	path, project := rv10Finished(t)
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	old := upgradeAgent
	entered := make(chan struct{}, 1)
	upgradeAgent = func(string) (string, error) { entered <- struct{}{}; <-gate; return "upgraded", nil }
	t.Cleanup(func() { release(); upgradeAgent = old })

	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	ui2 := &fakeUI{}
	rt2, err := Resume(testConfig(model.srv.URL), Options{Project: project, Prompt: "/upgrade"}, ui2, path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := contextWithCancel()
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- rt2.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the upgrade never began")
	}
	at := time.Now()
	rt2.post(func() { rt2.beginShutdown("interrupted", 130) })
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the session did not end")
	}
	took := time.Since(at)
	t.Logf("ended %v after the interrupt; logs %q", took, ui2.logged())
	if took > 2*time.Second || rv10Logged(ui2, "did not stop in time") {
		t.Errorf("the end waited %v for /upgrade's goroutine; logs %q", took, ui2.logged())
	}
}
