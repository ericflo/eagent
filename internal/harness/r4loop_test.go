package harness

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
)

// owedPhrase is in the reminder about a message kept for eagent (owedTail).
const owedPhrase = "has had no answer and seen nothing done about it"

// resumeOneCall resumes a stopped session's log with one call to a turn,
// interactively, on the same model.
func resumeOneCall(t *testing.T, path, project string, model *scripted) *session {
	t.Helper()
	seat := newFakeSeat("unused")
	useFakeRelays(t, seat)
	cfg := testConfig(model.srv.URL)
	cfg.MaxCallsPerTurn = 1
	ui := &fakeUI{input: make(chan string, 8)}
	rt, err := Resume(cfg, Options{Project: project, Interactive: true}, ui, path)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, rt: rt, ui: ui, seat: seat, model: model, done: make(chan int, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s.cancel = cancel
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	return s
}

func gaveUp(s *session) bool {
	for _, l := range s.ui.logged() {
		if strings.Contains(l, "went unanswered") {
			return true
		}
	}
	return false
}

// A message for eagent that the model held on, with no call left in the
// turn, is still owed after the session is stopped and resumed: the
// resumed session asks about it on the delays that were left, and gives up
// after the same number of reminders as a session that never stopped. A
// further resume does not start the reminders over.
func TestAQuestionHeldOnIsAskedAboutAgainAfterAResume(t *testing.T) {
	old := owedRetryDelays
	owedRetryDelays = []time.Duration{time.Hour, time.Hour, time.Hour}
	defer func() { owedRetryDelays = old }()
	var reminders atomic.Int32
	brain := func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), owedPhrase) && strings.Contains(lastUserText(msgs), "what is Codex up to?") {
			reminders.Add(1)
		}
		return hold()
	}
	s := startSession(t, "", brain, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 1 })
	s.settled()
	s.type_("@e what is Codex up to?")
	s.waitFor("the reminder to be armed", func() bool {
		var armed bool
		s.rt.sync(func() { armed = s.rt.turns.owed > 0 && s.rt.turns.owedTimer != nil })
		return armed
	})
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()

	owedRetryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	s2 := resumeOneCall(t, path, project, s.model)
	s2.waitFor("the question to be asked about again", func() bool { return reminders.Load() >= 1 })
	s2.waitFor("eagent to give up", func() bool { return gaveUp(s2) })
	time.Sleep(10 * owedRetryDelays[0])
	s2.settled()
	if n := reminders.Load(); n != int32(len(owedRetryDelays)) {
		t.Fatalf("the resumed session reminded the model %d times, want %d", n, len(owedRetryDelays))
	}
	s2.stop()

	s3 := resumeOneCall(t, path, project, s.model)
	s3.settled()
	time.Sleep(10 * owedRetryDelays[0])
	s3.settled()
	s3.stop()
	if n := reminders.Load(); n != int32(len(owedRetryDelays)) {
		t.Fatalf("a second resume started the reminders over: %d in all, want %d", n, len(owedRetryDelays))
	}
}

// Stopping a session while each reminder is in flight, and resuming it,
// over and over, does not call a model that always holds without end: a
// reminder that was started counts, whether or not it came back.
func TestStoppingDuringEachReminderDoesNotStartThemOver(t *testing.T) {
	old := owedRetryDelays
	owedRetryDelays = []time.Duration{time.Hour, time.Hour, time.Hour}
	defer func() { owedRetryDelays = old }()
	var reminders atomic.Int32
	never := make(chan struct{})
	defer close(never)
	brain := func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), owedPhrase) {
			reminders.Add(1)
			return reply{block: never, calls: hold().calls}
		}
		return hold()
	}
	s := startSession(t, "", brain, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 1 })
	s.settled()
	s.type_("@e what is Codex up to?")
	s.waitFor("the reminder to be armed", func() bool {
		var armed bool
		s.rt.sync(func() { armed = s.rt.turns.owed > 0 && s.rt.turns.owedTimer != nil })
		return armed
	})
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()
	owedRetryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	for run := 0; run < 2*len(owedRetryDelays)+2; run++ {
		before := reminders.Load()
		sn := resumeOneCall(t, path, project, s.model)
		// Once the loop runs, a reminder still to come is armed or on its
		// way; with none, fifteen of its delays show that none comes.
		var pending bool
		sn.rt.sync(func() { pending = sn.rt.turns.owedTimer != nil || sn.rt.turns.busy || sn.rt.wakePending() })
		wait := 600 * time.Millisecond
		if !pending {
			wait = 15 * owedRetryDelays[0]
		}
		deadline := time.Now().Add(wait)
		for time.Now().Before(deadline) && reminders.Load() == before {
			time.Sleep(5 * time.Millisecond)
		}
		sn.stop()
	}
	if n := reminders.Load(); n != int32(len(owedRetryDelays)) {
		t.Fatalf("a model stopped during every reminder was reminded %d times, want %d", n, len(owedRetryDelays))
	}
}

// Two messages for eagent that arrive in one wake are both owed when the
// model holds: the reminder quotes each, oldest first.
func TestAReminderNamesEveryMessageOfTheWakeOldestFirst(t *testing.T) {
	oldD, oldS := owedRetryDelays, wakeSettle
	owedRetryDelays = []time.Duration{30 * time.Millisecond, time.Hour, time.Hour}
	wakeSettle = 300 * time.Millisecond
	defer func() { owedRetryDelays, wakeSettle = oldD, oldS }()
	var mu sync.Mutex
	var reminder string
	brain := func(_ string, msgs []map[string]any) reply {
		if last := lastUserText(msgs); strings.Contains(last, "has had no answer") {
			mu.Lock()
			if reminder == "" {
				reminder = last
			}
			mu.Unlock()
		}
		return hold()
	}
	s := startSession(t, "", brain, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 1 })
	defer s.stop() // before the delays are put back
	s.settled()
	s.type_("@e did the tests pass?")
	s.type_("@e and is the deploy done?")
	s.waitFor("the reminder", func() bool { mu.Lock(); defer mu.Unlock(); return reminder != "" })
	mu.Lock()
	got := reminder
	mu.Unlock()
	i, j := strings.Index(got, "did the tests pass?"), strings.Index(got, "and is the deploy done?")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("the reminder does not name both messages, oldest first: %q", got)
	}
}

// A message kept for eagent that the model did not deal with stays owed
// when a second one arrives in the same turn after the record has moved
// past the first: the reminder names both.
func TestAMessageArrivingMidTurnDoesNotTakeTheOwedPlaceOfAnOlderOne(t *testing.T) {
	old := owedRetryDelays
	owedRetryDelays = []time.Duration{30 * time.Millisecond, time.Hour, time.Hour}
	defer func() { owedRetryDelays = old }()
	var mu sync.Mutex
	var reminder string
	var sTyped func(string)
	titled := false
	brain := func(_ string, msgs []map[string]any) reply {
		last := lastUserText(msgs)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(last, "has had no answer"):
			if reminder == "" {
				reminder = last
			}
		case strings.Contains(allText(msgs), "did the tests pass?") && !titled && msgs[len(msgs)-1]["role"] != "tool":
			// An act that is not an answer: the turn goes on the record,
			// and the second message arrives while it runs.
			titled = true
			sTyped("@e and is the deploy done?")
			time.Sleep(100 * time.Millisecond)
			return reply{calls: []event.ToolCall{tc("update_thread", `{"title":"Tests and deploy"}`)}}
		}
		return hold()
	}
	s := startSession(t, "", brain, nil)
	defer s.stop()
	mu.Lock()
	sTyped = s.type_
	mu.Unlock()
	s.settled()
	s.type_("@e did the tests pass?")
	s.waitFor("the reminder", func() bool { mu.Lock(); defer mu.Unlock(); return reminder != "" })
	mu.Lock()
	got := reminder
	mu.Unlock()
	i, j := strings.Index(got, "did the tests pass?"), strings.Index(got, "and is the deploy done?")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("the reminder does not name both messages, oldest first: %q", got)
	}
}

// The user's answer to eagent's question, held on in a turn with no call
// left, keeps its one later reminder across a stop and resume, and a
// second resume does not give it another.
func TestAnAnswerHeldOnIsBroughtUpOnceAfterAResume(t *testing.T) {
	old := owedRetryDelays
	owedRetryDelays = []time.Duration{time.Hour, time.Hour, time.Hour}
	defer func() { owedRetryDelays = old }()
	var reminded atomic.Int32
	brain := func(_ string, msgs []map[string]any) reply {
		last := lastUserText(msgs)
		switch {
		case strings.Contains(last, "nothing has been done with the answer"):
			reminded.Add(1)
		case strings.Contains(last, "kept for you") && msgs[len(msgs)-1]["role"] != "tool":
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge to main, or open a pull request?"}`)}}
		}
		return hold()
	}
	s := startSession(t, "", brain, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 1 })
	s.settled()
	s.type_("@e what next?")
	s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
	s.settled()
	s.type_("main")
	s.waitFor("the reminder to be armed", func() bool {
		var armed bool
		s.rt.sync(func() { armed = s.rt.turns.owedAnswer && s.rt.turns.owedTimer != nil })
		return armed
	})
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()

	owedRetryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	s2 := resumeOneCall(t, path, project, s.model)
	s2.waitFor("the answer to be brought up", func() bool { return reminded.Load() >= 1 })
	time.Sleep(10 * owedRetryDelays[0])
	s2.settled()
	var owed int64
	s2.rt.sync(func() { owed = s2.rt.turns.owed })
	if n := reminded.Load(); n != 1 || owed != 0 {
		t.Fatalf("the answer was brought up %d times (want once) and is owed %d after it", n, owed)
	}
	s2.stop()
	s3 := resumeOneCall(t, path, project, s.model)
	s3.settled()
	time.Sleep(10 * owedRetryDelays[0])
	s3.settled()
	s3.stop()
	if n := reminded.Load(); n != 1 {
		t.Fatalf("a second resume brought the answer up again: %d reminders", n)
	}
}

// The record of what is owed renders nothing: eagent's conversation is the
// same with it as without it, so writing it costs no prompt cache. And a
// session whose model answers writes none.
func TestTheDebtOnTheRecordLeavesTheConversationAsItWas(t *testing.T) {
	old := owedRetryDelays
	owedRetryDelays = []time.Duration{30 * time.Millisecond, 30 * time.Millisecond, 30 * time.Millisecond}
	defer func() { owedRetryDelays = old }()
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 1 })
	defer s.stop()
	s.settled()
	s.type_("@e what is Codex up to?")
	s.waitFor("eagent to give up", func() bool { return gaveUp(s) })
	s.settled()
	var all, without []event.Event
	s.read(func(st *state.State) { all = append(all, st.Events...) })
	for _, ev := range all {
		if ev.Type != event.Owed {
			without = append(without, ev)
		}
	}
	if len(without) == len(all) {
		t.Fatal("a held-on message left no record of the debt")
	}
	a, _ := json.Marshal(state.Replay(all).View(nil))
	b, _ := json.Marshal(state.Replay(without).View(nil))
	if string(a) != string(b) {
		t.Fatalf("the record of the debt changes the conversation:\n%s\n%s", a, b)
	}

	s2 := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			return say("Codex is idle.")
		}
		return hold()
	}, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 1 })
	defer s2.stop()
	s2.settled()
	s2.type_("@e what is Codex up to?")
	s2.waitFor("the answer", func() bool { return s2.ui.messageCount() == 1 })
	s2.settled()
	if n := len(s2.events(event.Owed)); n != 0 {
		t.Fatalf("a session whose model answered wrote %d records of a debt", n)
	}
}
