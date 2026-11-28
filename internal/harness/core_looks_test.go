package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// Ports of the old narratorcheck_test.go and token_efficiency_test.go: the
// rules of the old narrator's progress checks, kept in the one loop's
// progress looks, and the prompt-cache discipline.

// coreWorking starts a session whose agent takes a message and keeps
// working on it, with progress looks under the test's control.
func coreWorking(t *testing.T, brain func(model string, msgs []map[string]any) reply) *session {
	t.Helper()
	old := narrateGap
	narrateGap = 0
	t.Cleanup(func() { narrateGap = old })
	s := startSession(t, "", brain, func(cfg *config.Config) { cfg.QuietSeconds = 3600 })
	s.seat.mu.Lock()
	s.seat.hang = true
	s.seat.mu.Unlock()
	s.type_("List the open PRs and check their CI.")
	s.waitFor("the message to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	return s
}

// Old TestProgressCheckOnARelay, the parts TestProgressLooksFollowTheWork
// does not hold: a look that holds leaves no trace but its cost; a look
// that speaks is an ordinary turn on the record, opened by the cost of the
// silent looks before it and the steer it was given, and only that look is
// in the conversation afterwards; and when nothing new has shown for the
// quiet limit, the look says so and names what is still running.
func TestCoreALookThatSpeaksIsRecordedWithItsSteerAndAStallIsNamed(t *testing.T) {
	var speak atomic.Value
	speak.Store("")
	var seen tails
	s := coreWorking(t, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		if strings.Contains(tail, "A look while the work goes on.") && msgs[len(msgs)-1]["role"] != "tool" {
			seen.add(tail)
			if text, _ := speak.Load().(string); text != "" {
				return say(text)
			}
		}
		return hold()
	})
	step := func(text string) {
		t.Helper()
		var before int
		s.rt.sync(func() { before, _ = s.rt.relays.stepped(time.Time{}) })
		s.seat.emit(agentDoes(text))
		s.waitFor("the step "+text, func() bool { n, _ := s.rt.relays.stepped(time.Time{}); return n > before })
	}
	look := func() {
		t.Helper()
		base := s.model.count(testModel)
		s.rt.sync(func() { s.rt.maybeLook() })
		s.waitFor("a look", func() bool { return s.model.count(testModel) > base })
		s.settled()
	}

	// A look that holds.
	step("running gh pr checks 42")
	before := len(s.events())
	look()
	if n := len(s.events()); n != before {
		t.Fatalf("a held look left %d events", n-before)
	}
	if tail := seen.find("A look while"); !strings.Contains(tail, "running gh pr checks 42") || !strings.Contains(tail, "What Codex did since the user last heard from you") && !strings.Contains(tail, "What Codex did so far") {
		t.Fatalf("the look's tail = %q", tail)
	}
	var silent int
	s.rt.sync(func() { silent = s.rt.turns.silent.Count })
	if silent < 1 {
		t.Fatal("the held look's cost was not kept")
	}

	// A look that speaks.
	speak.Store("Codex is checking CI on #42; it edited ci.yml.")
	step("editing .github/workflows/ci.yml")
	look()
	s.waitFor("the progress line", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	var types []string
	var steer event.SteerData
	var checks event.ChecksData
	for _, ev := range s.events()[before:] {
		types = append(types, ev.Type)
		switch ev.Type {
		case event.Steer:
			_ = ev.Decode(&steer)
		case event.Checks:
			_ = ev.Decode(&checks)
		}
	}
	want := []string{event.Checks, event.TurnStart, event.Steer, event.Assistant, event.Message, event.ToolResult, event.TurnEnd}
	if strings.Join(types, " ") != strings.Join(want, " ") {
		t.Fatalf("recorded %v, want %v", types, want)
	}
	if checks.Count != silent || checks.Usage.Input == 0 {
		t.Fatalf("the silent looks were recorded as %+v, want %d of them", checks, silent)
	}
	if steer.Text != seen.find("editing .github") {
		t.Fatalf("the recorded steer is not the tail the model answered:\n%s\n---\n%s", steer.Text, seen.find("editing .github"))
	}
	var view string
	s.rt.sync(func() {
		for _, m := range s.rt.st.View(&s.rt.view) {
			view += m.Text + "\n"
		}
	})
	if n := strings.Count(view, "A look while the work goes on."); n != 1 {
		t.Fatalf("the conversation holds %d looks, want only the one that spoke", n)
	}

	// Nothing new for longer than the quiet limit.
	speak.Store("Codex is still on the CI workflow, four minutes in.")
	s.rt.sync(func() {
		s.rt.relays.mu.Lock()
		for name, list := range s.rt.relays.steps {
			for i := range list {
				list[i].at = time.Now().Add(-10 * time.Minute)
			}
			s.rt.relays.steps[name] = list
		}
		s.rt.relays.mu.Unlock()
		s.rt.activeSettings.QuietSeconds = 60
		s.rt.turns.saidAt = time.Now().Add(-4 * time.Minute)
		s.rt.turns.lookedAt = time.Time{}
	})
	look()
	s.waitFor("the still-working line", func() bool { return s.ui.messageCount() == 2 })
	tail := seen.find("Nothing new has shown since you last spoke")
	if !strings.Contains(tail, "Nothing new has shown since you last spoke, 4m ago") || !strings.Contains(tail, "Codex has started no new step") || !strings.Contains(tail, "Right now: editing .github/workflows/ci.yml") {
		t.Fatalf("the stalled look's tail = %q", tail)
	}
}

// Old TestCheckSteerSaysWhatToDo: what a progress look's tail tells the
// model, case by case: the steps since the user last heard (the oldest
// left out past a bound, repeats counted), how long ago that was and when
// to hold; past the quiet limit, to send a progress line now; with nothing
// new, to say what is still running and for how long, not a progress line;
// and before the user has heard anything, to tell them now. (The count of
// task workers still running is gone with them.)
func TestCoreLookTailSaysWhatToDo(t *testing.T) {
	s := coreWorking(t, func(string, []map[string]any) reply { return hold() })
	s.rt.sync(func() {
		r := s.rt
		now := time.Now()
		savedSaid, savedQuiet := r.turns.saidAt, r.activeSettings.QuietSeconds
		defer func() { r.turns.saidAt, r.activeSettings.QuietSeconds = savedSaid, savedQuiet }()
		r.activeSettings.QuietSeconds = 180
		at := now.Add(-time.Minute)
		var steps []relayStep
		for i := 0; i < 40; i++ {
			steps = append(steps, relayStep{text: "running go test ./internal/harness/", at: at, n: 1 + i%2})
		}
		r.relays.mu.Lock()
		r.relays.steps = map[string][]relayStep{"codex": steps}
		r.relays.mu.Unlock()

		r.turns.saidAt = now.Add(-70 * time.Second)
		got := r.tail(wake{look: true, moved: true}, now)
		for _, want := range []string{"(10 earlier steps left out)", "- " + at.In(r.st.Location()).Format("15:04") + " running go test ./internal/harness/ (x2)", "The user last heard from you 1m ago", "Hold only if"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in %q", want, got)
			}
		}

		r.turns.saidAt = now.Add(-4 * time.Minute)
		if got := r.tail(wake{look: true, moved: true}, now); !strings.Contains(got, "has heard nothing for 4m") || !strings.Contains(got, "send a progress line now") {
			t.Errorf("overdue tail %q", got)
		}

		r.relays.mu.Lock()
		r.relays.steps = map[string][]relayStep{"codex": {{text: "running make check", at: now.Add(-6 * time.Minute), n: 1}}}
		if r.relays.activity == nil {
			r.relays.activity = map[string]relayActivity{}
		}
		r.relays.activity["codex"] = relayActivity{text: "running make check", at: now.Add(-6 * time.Minute)}
		r.relays.mu.Unlock()
		if got := r.tail(wake{look: true}, now); !strings.Contains(got, "Nothing new has shown since you last spoke, 4m") || !strings.Contains(got, "Right now: running make check (for 6m)") || strings.Contains(got, "progress line now") {
			t.Errorf("stalled tail %q", got)
		}

		r.turns.saidAt = time.Time{}
		if got := r.tail(wake{look: true, moved: true}, now); !strings.Contains(got, "The user has heard nothing from you in the") || !strings.Contains(got, "tell them now") {
			t.Errorf("first tail %q", got)
		}
	})
}

// Old TestNarratorSystemPromptStaticAcrossPhoneRemoteMode: the system
// prompt is the same bytes for the whole run: the phone thread attaching,
// or the user saying they are away from the terminal, changes nothing in
// it and wakes no one.
func TestCoreSystemPromptStaysPutWhenThePhoneThreadChanges(t *testing.T) {
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			return say("Here.")
		}
		return hold()
	}, nil)
	s.type_("@e hello")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	system, base := s.rt.system, s.model.count(testModel)
	s.rt.sync(func() {
		s.rt.append(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t1", RemoteMode: true}))
	})
	s.settled()
	if got := s.model.count(testModel); got != base {
		t.Fatalf("the phone thread's event made %d model calls", got-base)
	}
	if s.rt.system != system || strings.Contains(strings.ToLower(system), "remote mode") {
		t.Fatal("the system prompt changed with the phone thread, or speaks of remote mode")
	}
	s.type_("@e and now?")
	s.waitFor("the second answer", func() bool { return s.ui.messageCount() == 2 })
	reqs := s.model.requests(testModel)
	if string(reqs[len(reqs)-1].Messages[0]) != string(reqs[0].Messages[0]) {
		t.Fatal("the system prompt sent changed during the run")
	}
}

// Old TestRoutineSteersOmittedOnMidTurnLoops: the lines written for a call
// are written once per turn: a second call in the same turn adds no new
// steer, only the results it asked for. (The orchestrator's working
// directory and context lines and the task worker's call countdown are gone
// with them.)
func TestCoreLaterCallsInATurnAddNoSteer(t *testing.T) {
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return say("Its screen shows the prompt; it is done.")
		case strings.Contains(lastUserText(msgs), "finished a turn"):
			return reply{calls: []event.ToolCall{tc("relay_screen", `{"lines":20}`)}}
		}
		return hold()
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	if n := len(s.events(event.Steer)); n != 1 {
		t.Fatalf("%d steers recorded for one turn, want 1", n)
	}
	reqs := s.model.requests(testModel)
	last := reqs[len(reqs)-1].Messages
	if !strings.Contains(string(last[len(last)-1]), `"role":"tool"`) {
		t.Fatalf("the second call of the turn ends with %s, want the tool's result", last[len(last)-1])
	}
	users := 0
	for _, m := range last {
		if strings.Contains(string(m), `"role":"user"`) {
			users++
		}
	}
	prevUsers := 0
	for _, m := range reqs[len(reqs)-2].Messages {
		if strings.Contains(string(m), `"role":"user"`) {
			prevUsers++
		}
	}
	if users != prevUsers {
		t.Fatalf("the second call has %d user messages, the first %d: a new steer was added", users, prevUsers)
	}
}
