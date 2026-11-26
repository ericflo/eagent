package harness

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/store"
)

// saidWith reports whether the user was shown a message holding part.
func saidWith(ui *fakeUI, part string) bool {
	for _, m := range ui.said() {
		if strings.Contains(m, part) {
			return true
		}
	}
	return false
}

// A batch session that finished (its agent stopped, the user told) is
// resumed with /restart. The start takes a while; the session must not end
// under it: the agent it brings up is kept, the user hears it restarted,
// and the session is still there when they do.
func TestResumingAFinishedSessionWithRestartKeepsTheRestartedAgent(t *testing.T) {
	rt, _, _ := batch(t, brief+" Stop when done.", "Done: #42 and #44 are merged.", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
			return stopsAndReports("Done: Codex merged #42 and #44 on main.")
		}
		return hold()
	})
	path, project := rt.sess.Path, rt.opts.Project

	seat2 := newFakeSeat("Back on it.")
	entered, gate := make(chan struct{}, 1), make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	old := launchRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		entered <- struct{}{}
		<-gate // the start takes its time
		return seat2, relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	t.Cleanup(func() { launchRelay = old })

	// The closing look the resumed session asks for is answered once the
	// start is under way, as a model slower than the command's first steps.
	var calls atomic.Int32
	looked, answer := make(chan struct{}, 1), make(chan struct{})
	model := newScripted(func(string, []map[string]any) reply {
		if calls.Add(1) == 1 {
			looked <- struct{}{}
			return reply{block: answer, calls: hold().calls}
		}
		return hold()
	})
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
	for _, ch := range []chan struct{}{entered, looked} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("the start or the closing look never began")
		}
	}
	close(answer)
	// Whatever the loop does once the look is answered, it has done.
	waitFor(t, "the closing look to be taken in", func() bool {
		select {
		case <-rt2.closed:
			return true
		default:
		}
		idle := false
		rt2.sync(func() {
			idle = !rt2.ending && !rt2.turns.busy && !rt2.summary.busy && len(rt2.later) == 0
		})
		return idle
	})
	release()
	waitFor(t, "the restart's outcome", func() bool {
		return saidWith(ui2, "Restarted") || seat2.wasKilled() || len(done) > 0
	})
	if seat2.wasKilled() {
		t.Fatalf("the restarted agent was killed: the session ended under its start; said %q", ui2.said())
	}
	if !saidWith(ui2, "Restarted") {
		t.Fatalf("the user was not told the agent restarted; said %q", ui2.said())
	}
	ended := true
	rt2.sync(func() { ended = rt2.ending || rt2.st.Ended })
	if ended || len(done) > 0 {
		t.Fatal("the session ended with the restarted agent running")
	}
}

// A batch session that stopped on a question nobody could answer is
// resumed with /relay. The session ends again (the question is still
// open), but only after the command ran and the user saw what it shows.
func TestResumingAnUnanswerableSessionWithRelayRunsTheCommand(t *testing.T) {
	rt, _, _ := batchExit(t, 2, brief, "Both are ready. Merge to main, or open a pull request?", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge to main, or open a pull request?"}`)}}
		}
		return hold()
	})
	path, project := rt.sess.Path, rt.opts.Project
	useFakeRelays(t, newFakeSeat("unused"))
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	ui2 := &fakeUI{}
	rt2, err := Resume(testConfig(model.srv.URL), Options{Project: project, Prompt: "/relay"}, ui2, path)
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
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (the question is still open)", code)
	}
	if !saidWith(ui2, "codex profile") {
		t.Fatalf("/relay was dropped: said %q", ui2.said())
	}
	evs, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	told, endAt := -1, -1
	for i, ev := range evs {
		var d event.MessageData
		if ev.Type == event.Message && ev.Decode(&d) == nil && strings.Contains(d.Text, "codex profile") {
			told = i
		}
		if ev.Type == event.SessionEnd {
			endAt = i
		}
	}
	if told < 0 || told > endAt {
		t.Fatalf("what /relay shows is at %d on the record, the session's end at %d", told, endAt)
	}
}
