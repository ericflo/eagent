package harness

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
)

// eventually waits for a condition checked on the loop goroutine.
func eventually(t *testing.T, rt *Runtime, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		rt.sync(func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// narratorLog lists the narrator's own events (and the checks recorded for
// it), by type. Loop goroutine.
func narratorLog(rt *Runtime) []string {
	var out []string
	for _, ev := range rt.st.Events {
		if ev.Actor == event.ActorNarrator {
			out = append(out, ev.Type)
		}
	}
	return out
}

func narratorSettled(rt *Runtime) bool {
	return !rt.narrBusy && rt.narrPending == "" && rt.userWaitSeq == 0
}

// checkBrain scripts a narrator that answers the user's message, and on a
// progress check holds or speaks as told, keeping the steers it was shown.
type checkBrain struct {
	speak  atomic.Value // the message to send on the next check; "" holds
	mu     sync.Mutex
	steers []string
}

func (c *checkBrain) narrator(msgs []map[string]any) reply {
	last := lastUserText(msgs)
	switch {
	case strings.Contains(last, "Progress check while work goes on"):
		c.mu.Lock()
		c.steers = append(c.steers, last)
		c.mu.Unlock()
		if text, _ := c.speak.Load().(string); text != "" {
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"`+text+`"}`)}}
		}
	case strings.Contains(last, "the user wrote"):
		return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Got it."}`)}}
	}
	return reply{calls: []event.ToolCall{tc("hold", `{}`)}}
}

func (c *checkBrain) steer(i int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i < 0 {
		i += len(c.steers)
	}
	if i < 0 || i >= len(c.steers) {
		return ""
	}
	return c.steers[i]
}

func (c *checkBrain) count() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.steers) }

func noNarrateGap(t *testing.T) {
	t.Helper()
	old := narrateGap
	narrateGap = 0
	t.Cleanup(func() { narrateGap = old })
}

// While a relayed agent works, a progress check shows the narrator what the
// agent did since the user last heard. Holding leaves nothing in the
// narrator's conversation, and no new step means no model call at all.
// Speaking puts the turn on the record with its steer. When nothing new has
// shown for the quiet limit, the narrator is asked to say what is still
// running.
func TestProgressCheckOnARelay(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	noNarrateGap(t)
	seat := newFakeSeat("done")
	seat.hang = true
	useFakeRelays(t, seat)
	cb := &checkBrain{}
	s := newScripted(func(model string, msgs []map[string]any) reply {
		if model == "orch" {
			return reply{calls: []event.ToolCall{tc("yield", `{"done":false,"reason":"waiting"}`)}}
		}
		return cb.narrator(msgs)
	})
	defer s.srv.Close()
	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := New(testConfig(s.srv.URL), Options{Project: t.TempDir(), Relay: "codex", Interactive: true}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runUntilCleanup(t, rt, ctx, cancel)
	ui.input <- "List the open PRs and check their CI."
	eventually(t, rt, "the agent to be working and the narrator to have answered", func() bool {
		rel := rt.st.Relays["codex"]
		return rel != nil && rel.State == "working" && rt.narrLastSaid != "" && narratorSettled(rt) && !rt.orchBusy
	})
	step := func(remark, activity string) {
		t.Helper()
		var before int
		rt.sync(func() { before, _ = rt.relays.stepped(time.Time{}) })
		if remark != "" {
			seat.events <- relay.Event{Kind: relay.KindProgress, Text: remark}
		}
		seat.events <- relay.Event{Kind: relay.KindActivity, Text: activity}
		eventually(t, rt, "the step "+activity, func() bool { n, _ := rt.relays.stepped(time.Time{}); return n > before })
	}
	check := func() {
		t.Helper()
		rt.sync(rt.maybeCheckProgress)
		eventually(t, rt, "the check to finish", func() bool { return !rt.narrBusy })
	}

	// A check that holds: the model was asked, the log shows nothing of it.
	step("Two PRs are open; checking CI on #42.", "running gh pr checks 42")
	var before []string
	rt.sync(func() { before = narratorLog(rt) })
	check()
	if cb.count() != 1 {
		t.Fatalf("checks made = %d, want 1", cb.count())
	}
	if st := cb.steer(0); !strings.Contains(st, "- ") || !strings.Contains(st, "running gh pr checks 42") || !strings.Contains(st, "What Codex did since your last message") || !strings.Contains(st, "Hold only if all of it is still the step you last described") {
		t.Fatalf("steer = %q", st)
	}
	var after []string
	var quiet int
	rt.sync(func() { after, quiet = narratorLog(rt), rt.quietChecks.Count })
	if len(after) != len(before) || quiet != 1 {
		t.Fatalf("a held check left a trace: before %v, after %v, counted %d", before, after, quiet)
	}
	// Nothing new since it looked: no model call.
	check()
	if cb.count() != 1 {
		t.Fatalf("a check with nothing new called the model (%d calls)", cb.count())
	}

	// A check that speaks is an ordinary turn on the record.
	cb.speak.Store("Codex found two open PRs and is checking CI on #42; it edited ci.yml.")
	step("", "editing .github/workflows/ci.yml")
	check()
	eventually(t, rt, "the progress line", func() bool { return strings.HasPrefix(rt.narrLastSaid, "Codex found two open PRs") })
	var tail []string
	var view string
	var checks event.ChecksData
	rt.sync(func() {
		log := narratorLog(rt)
		tail = log[len(before):]
		for _, m := range rt.st.NarratorView(nil) {
			view += m.Text + "\n"
		}
		for _, ev := range rt.st.Events {
			if ev.Type == event.Checks {
				_ = ev.Decode(&checks)
			}
		}
	})
	want := []string{event.Checks, event.TurnStart, event.Steer, event.Assistant, event.Message, event.ToolResult, event.TurnEnd}
	if strings.Join(tail, " ") != strings.Join(want, " ") {
		t.Fatalf("recorded %v, want %v", tail, want)
	}
	if checks.Count != 1 || checks.Usage.Input == 0 {
		t.Fatalf("silent checks recorded as %+v", checks)
	}
	// Only the check that spoke is in its conversation; the held one is not.
	if n := strings.Count(view, "Progress check while work goes on"); n != 1 {
		t.Fatalf("the narrator's view holds %d progress checks, want the one that spoke", n)
	}
	// The view the model answered is the view the log now rebuilds.
	if st := cb.steer(-1); !strings.Contains(view, st) {
		t.Fatalf("the recorded turn does not show the steer the model was given:\n%s\n---\n%s", st, view)
	}

	// Nothing new for longer than the quiet limit: it says what is still on.
	cb.speak.Store("Codex is still on the CI workflow, four minutes in.")
	rt.sync(func() {
		rt.relays.mu.Lock()
		for name, list := range rt.relays.steps {
			for i := range list {
				list[i].at = time.Now().Add(-10 * time.Minute)
			}
			rt.relays.steps[name] = list
		}
		rt.relays.mu.Unlock()
		rt.narrSaidAt = time.Now().Add(-4 * time.Minute)
		rt.checkedAt = time.Time{}
	})
	check()
	eventually(t, rt, "the still-working line", func() bool { return strings.HasPrefix(rt.narrLastSaid, "Codex is still on") })
	if st := cb.steer(-1); !strings.Contains(st, "Nothing new has shown since you last spoke") || !strings.Contains(st, "Codex has started no new step") {
		t.Fatalf("steer = %q", st)
	}
}

// The same goes for a session without a relay: while the orchestrator
// works, a held check leaves no trace, and past the quiet limit the
// narrator is told to speak.
func TestProgressCheckOnAnOrdinarySession(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	noNarrateGap(t)
	second, release := make(chan struct{}), make(chan struct{})
	cb := &checkBrain{}
	s := newScripted(func(model string, msgs []map[string]any) reply {
		if model != "orch" {
			return cb.narrator(msgs)
		}
		all := allText(msgs)
		switch {
		case !strings.Contains(all, "first-step-done"):
			return reply{calls: []event.ToolCall{tc("bash", `{"command":"echo first-step-done"}`)}}
		case !strings.Contains(all, "second-step-done"):
			return reply{block: second, calls: []event.ToolCall{tc("bash", `{"command":"echo second-step-done"}`)}}
		}
		return reply{block: release, calls: []event.ToolCall{tc("yield", `{"done":true,"reason":"both steps ran"}`)}}
	})
	defer s.srv.Close()
	defer close(release)
	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := New(testConfig(s.srv.URL), Options{Project: t.TempDir(), Prompt: "Run the two steps.", Interactive: true}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runUntilCleanup(t, rt, ctx, cancel)
	eventually(t, rt, "the first step and the narrator's first word", func() bool {
		return s.count("orch") >= 2 && rt.narrLastSaid != "" && narratorSettled(rt)
	})
	close(second)
	eventually(t, rt, "the second step", func() bool { return s.count("orch") >= 3 && rt.orchBusy && narratorSettled(rt) })

	var before, after []string
	rt.sync(func() { before = narratorLog(rt) })
	rt.sync(rt.maybeCheckProgress)
	eventually(t, rt, "the check to finish", func() bool { return !rt.narrBusy && cb.count() == 1 })
	rt.sync(func() { after = narratorLog(rt) })
	if len(after) != len(before) {
		t.Fatalf("a held check left a trace: %v then %v", before, after)
	}
	if st := cb.steer(0); !strings.Contains(st, "second-step-done") && !strings.Contains(st, "Tell them what has happened since") {
		t.Fatalf("steer = %q", st)
	}
	rt.sync(rt.maybeCheckProgress)
	time.Sleep(60 * time.Millisecond)
	if cb.count() != 1 {
		t.Fatalf("a check with nothing new called the model (%d calls)", cb.count())
	}

	// Past the quiet limit with work still going, it is told to speak.
	cb.speak.Store("Both echo steps ran; I am writing up the result.")
	rt.sync(func() { rt.narrSaidAt = time.Now().Add(-4 * time.Minute) })
	rt.sync(rt.maybeCheckProgress)
	eventually(t, rt, "the progress line", func() bool { return strings.HasPrefix(rt.narrLastSaid, "Both echo steps ran") && !rt.narrBusy })
	if st := cb.steer(-1); !strings.Contains(st, "send a progress line now") {
		t.Fatalf("steer = %q", st)
	}
	rt.sync(func() { after = narratorLog(rt) })
	if got := strings.Join(after[len(before):], " "); !strings.Contains(got, event.TurnStart+" "+event.Steer+" "+event.Assistant) {
		t.Fatalf("recorded %v", after[len(before):])
	}
}

func TestCheckSteerSaysWhatToDo(t *testing.T) {
	now := time.Date(2026, 11, 23, 14, 5, 0, 0, time.Local)
	base := checkSteer{now: now, started: now.Add(-20 * time.Minute), limit: 3 * time.Minute, last: "Claude is editing relay.go."}
	steps := make([]relayStep, 0, 40)
	for i := 0; i < 40; i++ {
		steps = append(steps, relayStep{text: "running go test ./internal/harness/", at: now.Add(-time.Minute), n: 1 + i%2})
	}
	s := base
	s.quiet, s.moved, s.relays = 70*time.Second, true, []relayProgress{{who: "Claude", steps: steps}}
	got := s.text()
	for _, want := range []string{"(10 earlier steps left out)", "- 14:04 running go test ./internal/harness/ (x2)", "The user last heard from you 1m ago", "Hold only if", "send_message or hold"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	s.quiet = 4 * time.Minute
	if got := s.text(); !strings.Contains(got, "has heard nothing for 4m") || !strings.Contains(got, "send a progress line now") {
		t.Fatalf("overdue steer %q", got)
	}
	s.moved, s.relays = false, []relayProgress{{who: "Claude"}}
	s.inflight = []string{"Claude has been working for 12m; right now: running make check (for 6m)"}
	if got := s.text(); !strings.Contains(got, "Nothing new has shown since you last spoke, 4m") || !strings.Contains(got, "running make check (for 6m)") || strings.Contains(got, "progress line now") {
		t.Fatalf("stalled steer %q", got)
	}
	first := checkSteer{now: now, started: now.Add(-90 * time.Second), quiet: 90 * time.Second, limit: 3 * time.Minute, moved: true, tasks: 2}
	if got := first.text(); !strings.Contains(got, "has heard nothing from you in the 1m since they asked") || !strings.Contains(got, "2 task(s) are still running") {
		t.Fatalf("first steer %q", got)
	}
}
