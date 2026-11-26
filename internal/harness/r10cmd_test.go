package harness

import (
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/relay"
)

// A resumed batch session is interrupted (ended by its user) while a
// /restart's start is under way and takes 7 s. No model call is in flight
// (waited for below); finish must not say one did not stop in time. At
// 59a2069 the command's goroutine was not in r.wg, so this line could only
// come from a turn.
func TestRV10fInterruptDuringSlowRestartLogsNoModelCall(t *testing.T) {
	path, project := rv10Finished(t)
	seat2 := newFakeSeat("Back on it.")
	entered, gate := make(chan struct{}, 1), make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	old := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
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
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- rt2.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the start never began")
	}
	waitFor(t, "no model call in flight", func() bool {
		busy := true
		rt2.sync(func() { busy = rt2.turns.busy })
		return !busy
	})
	at := time.Now()
	rt2.post(func() { rt2.beginShutdown("interrupted", 130) })
	time.AfterFunc(7*time.Second, release)
	select {
	case <-done:
	case <-time.After(40 * time.Second):
		t.Fatal("the session did not end")
	}
	t.Logf("ended %v after the interrupt; said %q; logs %q", time.Since(at), ui2.said(), ui2.logged())
	if rv10Logged(ui2, "a model call did not stop in time") {
		t.Errorf("finish logged %q though no model call was in flight: /restart's goroutine held r.wg", "a model call did not stop in time")
	}
}
