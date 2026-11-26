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
// minutes. The session ends under it (as at 59a2069). Time how long the
// end now waits, and what it says about it.
func TestRV10UpgradeOnResumedFinishedSession(t *testing.T) {
	path, project := rv10Finished(t)
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	old := upgradeAgent
	entered := make(chan struct{}, 1)
	upgradeAgent = func(string) (string, error) { entered <- struct{}{}; <-gate; return "upgraded", nil }
	t.Cleanup(func() {
		// The upgrade's goroutine is not waited for by the session's end:
		// it read the stub before entering it, so it is put back after.
		release()
		select {
		case <-entered:
		case <-time.After(time.Second):
		}
		upgradeAgent = old
	})

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
	start := time.Now()
	go func() { done <- rt2.Run(ctx) }()
	var endingAt time.Time
	waitFor(t, "the session to begin ending", func() bool {
		e := false
		rt2.sync(func() { e = rt2.ending })
		select {
		case <-rt2.closed:
			e = true
		default:
		}
		if e && endingAt.IsZero() {
			endingAt = time.Now()
		}
		return e
	})
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	end := time.Now()
	t.Logf("Run took %v; from ending to Run's return %v; said %q; logs %q", end.Sub(start), end.Sub(endingAt), ui2.said(), ui2.logged())
	if rv10Logged(ui2, "a model call did not stop in time") {
		t.Errorf("finish logged %q though no model call was in flight (a command's goroutine held r.wg); finish waited %v", "a model call did not stop in time", end.Sub(endingAt))
	}
	// Nothing is told about the upgrade in a session that ends under it,
	// as at 59a2069 (listed as a known limit).
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

// The session is interrupted while its closing look is being written and
// a command's work is under way. The look ends at once; how long does the
// end wait now?
func TestRV10InterruptDuringClosingLookWithSlowCommand(t *testing.T) {
	path, project := rv10Finished(t)
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	old := upgradeAgent
	entered := make(chan struct{}, 1)
	upgradeAgent = func(string) (string, error) { entered <- struct{}{}; <-gate; return "upgraded", nil }
	t.Cleanup(func() {
		// The upgrade's goroutine is not waited for by the session's end:
		// it read the stub before entering it, so it is put back after.
		release()
		select {
		case <-entered:
		case <-time.After(time.Second):
		}
		upgradeAgent = old
	})

	looked, answer := make(chan struct{}, 1), make(chan struct{})
	first := sync.OnceFunc(func() { looked <- struct{}{} })
	model := newScripted(func(string, []map[string]any) reply {
		first()
		return reply{block: answer, calls: hold().calls}
	})
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
	case <-looked:
	case <-time.After(5 * time.Second):
		t.Fatal("no closing look")
	}
	at := time.Now()
	rt2.post(func() { rt2.beginShutdown("interrupted", 130) })
	close(answer)
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the session did not end")
	}
	took := time.Since(at)
	t.Logf("ended %v after the interrupt (finalReportGrace %v); logs %q", took, finalReportGrace, ui2.logged())
	if took > 2*time.Second {
		t.Errorf("the closing look was answered at once, but the end waited %v (the full finalReportGrace, 75 s in production) for /upgrade's goroutine", took)
	}
}
