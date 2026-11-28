package harness

import (
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/relay"
)

// When the restart a refusal called for could not be made (the file for
// the next conversation could not be created), the conversation in use was
// never restarted: the user is not told it is refused "even after I
// restarted it from my notes", but what any failed call tells them.
func TestRefusedAfterAFailedRestartIsNotCalledRestarted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made read-only for root")
	}
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var tooLong, writing atomic.Bool
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			writing.Store(true)
			p := places(ask)
			return reply{block: gate, text: briefing(p[:min(len(p), 4)])}
		}
		if tooLong.Load() {
			return reply{status: 400, body: tooLongBody}
		}
		return hold()
	}, nil)
	t.Cleanup(release)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	tooLong.Store(true)
	s.type_("@e are you there?")
	s.waitFor("the dossier call to go out", writing.Load)
	dir := s.rt.sess.Path
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	release()
	told := func() string {
		for _, m := range s.ui.said() {
			if strings.Contains(m, "refuses my conversation") || strings.Contains(m, "can't reach my own model") || strings.Contains(m, "refuses my calls") {
				return m
			}
		}
		return ""
	}
	s.waitFor("the user to be told about the failing calls", func() bool { return told() != "" })
	if n := len(s.events(event.SubsessionStart)); n != 1 {
		t.Fatalf("%d conversations begun; the restart was meant to fail", n)
	}
	if m := told(); strings.Contains(m, "restarted it from my notes") {
		t.Fatalf("the user was told of a restart that was never made: %q", m)
	}
}

// After restarts in a row stopped on a 60k model, that model's provider
// stops serving it and the session falls back to the 128k model of its
// chain. As for a 128k model picked in settings, the 128k one refuses the
// conversation as it stands, one restart on it makes room, and that
// restart is made: what the 60k model needed is not counted against it.
func TestAFallbackWithALargerWindowGetsItsOwnRestart(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	var restarted, gone atomic.Bool
	var refused atomic.Int64
	fallback := newScripted(func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			restarted.Store(true)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		if !restarted.Load() {
			refused.Add(1)
			return r7TooLong
		}
		return r7RestartLooks(m, 5000)
	})
	t.Cleanup(fallback.srv.Close)
	brain := func(_ string, m []map[string]any) reply {
		if gone.Load() {
			return reply{status: 401, body: `{"error":{"message":"invalid api key","type":"authentication_error"}}`}
		}
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		if strings.HasPrefix(lastUserText(m), "[eagent] That reply had no tool call") {
			return r7TooLong
		}
		return reply{text: "let me think about that"}
	}
	s := startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 60000, 2048
		c.Model.Fallback = &config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: fallback.srv.URL, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: 128_000, MaxTokens: 2048}
	})
	r5LogOnFailure(s)
	s.settled()
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	r6Until(t, "the fullGrew notice", func() bool {
		return r5Told(s, fullNotice+": after several restarts in a row") > 0 || r6Dossiers(s) > restartRescueMax+3
	})
	if r5Told(s, fullNotice+": after several restarts in a row") == 0 {
		t.Fatalf("no fullGrew notice")
	}
	before := r6Dossiers(s)
	gone.Store(true)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"}) })
	deadline := time.Now().Add(5 * time.Second)
	for r5Told(s, "Noted.") == 0 && r5Told(s, stillFullNotice+`"still there?"`) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if r5Told(s, "Noted.") == 0 {
		t.Fatalf("on the 128k fallback (asked %d times): refused %d times, %d restarts, told the message waits %d times; the message is unanswered, though one restart makes room",
			fallback.count("other-model"), refused.Load(), r6Dossiers(s)-before, r5Told(s, stillFullNotice))
	}
}

// r10Runs counts the results recorded for a tool: each is one run of it.
func r10Runs(s *session, name string) int {
	n := 0
	for _, ev := range s.events(event.ToolResult) {
		var d event.ToolResultData
		if ev.Decode(&d) == nil && d.Name == name {
			n++
		}
	}
	return n
}

// r10Bad is a call that fails, and is not one the client tries again
// itself: what a turn gets from a provider that keeps failing.
var r10Bad = reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}

// A message to the agent did not go. eagent reads the screen, a call that
// asks for another, and that next call is refused as too long. The
// conversation is restarted, and then eagent is called again to carry on:
// the message goes, and the screen was read once.
func TestACarryOnRefusedAsTooLongIsMadeAfterTheRestart(t *testing.T) {
	var phase atomic.Int32
	var stuck atomic.Pointer[fakeSeat] // the agent, once its dialog is to be cleared
	s := startSession(t, "", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			phase.CompareAndSwap(3, 4)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case phase.Load() == 1 && strings.Contains(allText(m), "did not go"):
			phase.Store(2)
			return reply{calls: []event.ToolCall{tc("relay_screen", `{}`)}}
		case phase.Load() == 2:
			phase.Store(3)
			stuck.Load().setSendErr(nil) // what was on its screen has gone
			return r7TooLong             // the screen it read did not fit
		case phase.CompareAndSwap(4, 5):
			return reply{calls: []event.ToolCall{tc("relay_send", `{"text":"deploy to staging"}`)}}
		}
		return hold()
	}, nil)
	s.type_("start on the release")
	s.waitFor("the message to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	s.seat.setSendErr(errors.New("the agent is showing something that needs an answer first"))
	stuck.Store(s.seat)
	phase.Store(1)
	s.type_("deploy to staging")
	s.waitFor("the restart", func() bool { return len(s.dossiers()) == 1 })
	s.waitFor("the message sent after the restart", func() bool { return len(s.seat.sentTexts()) == 2 })
	s.settled()
	if n := r10Runs(s, "relay_screen"); n != 1 {
		t.Fatalf("the screen was read %d times, want once", n)
	}
	if sent := s.seat.sentTexts(); len(sent) != 2 || sent[1] != "deploy to staging" {
		t.Fatalf("typed into the agent: %q", sent)
	}
}

// r10HeldSchedule has the user write to eagent; eagent sets a timer, a call
// that asks for another, and that next call fails. It returns the session,
// and the phase the model is in (3 once the failure is out; answering
// moves it to 4).
func r10HeldSchedule(t *testing.T) (*session, *atomic.Int32) {
	t.Helper()
	var phase atomic.Int32
	s := startSession(t, brief, func(_ string, m []map[string]any) reply {
		switch {
		case phase.Load() == 1 && strings.Contains(lastUserText(m), "kept for you"):
			phase.Store(2)
			return reply{calls: []event.ToolCall{tc("schedule", `{"spec":"1h","note":"Remind the user to check staging."}`)}}
		case phase.Load() == 2:
			phase.Store(3)
			return r10Bad
		case phase.CompareAndSwap(3, 4):
			return say("I'll remind you to check staging in an hour.")
		}
		return hold()
	}, nil)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	phase.Store(1)
	s.type_("@e remind me to check staging in an hour")
	s.waitFor("the failed call", func() bool { return phase.Load() >= 3 && len(s.events(event.Error)) == 1 })
	return s, &phase
}

// The failed call is tried again: the user's message to eagent is
// answered, and the timer is set once.
func TestACarryOnThatFailedIsTriedAgain(t *testing.T) {
	s, _ := r10HeldSchedule(t)
	s.waitFor("the answer", func() bool { return r5Told(s, "I'll remind you") == 1 })
	s.settled()
	if n := len(s.events(event.ScheduleCreate)); n != 1 {
		t.Fatalf("%d timers set, want one", n)
	}
}

// The same, stopped before the failed call is tried again (it waits an
// hour): the resumed session tries it, and the timer is still set once.
func TestACarryOnThatFailedIsTriedAgainOnResume(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Hour})
	s, phase := r10HeldSchedule(t)
	s.waitFor("the turn to end", func() bool {
		busy := true
		s.rt.sync(func() { busy = s.rt.turns.busy })
		return !busy && len(s.events(event.TurnEnd)) > 0
	})
	if phase.Load() != 3 || r5Told(s, "I'll remind you") != 0 {
		t.Fatalf("tried again before the stop (phase %d)", phase.Load())
	}
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()
	ui2 := &fakeUI{input: make(chan string, 4)}
	rt2, err := Resume(testConfig(s.model.srv.URL), Options{Project: project, Interactive: true}, ui2, path)
	if err != nil {
		t.Fatal(err)
	}
	s2 := &session{t: t, rt: rt2, ui: ui2, seat: s.seat, model: s.model, done: make(chan int, 1)}
	ctx, cancel := contextWithCancel()
	s2.cancel = cancel
	go func() { s2.done <- rt2.Run(ctx) }()
	t.Cleanup(s2.stop)
	s2.waitFor("the answer after the resume", func() bool { return r5Told(s2, "I'll remind you") == 1 })
	s2.settled()
	if n := len(s2.events(event.ScheduleCreate)); n != 1 {
		t.Fatalf("%d timers set, want one", n)
	}
}

// An agent is stuck on a dialog. eagent reads its screen, a call that asks
// for another, and that next call fails. It is tried again, and the agent
// gets its keys; the screen was read once.
func TestACarryOnForAStuckAgentIsTriedAgain(t *testing.T) {
	var phase atomic.Int32
	s := startSession(t, brief, func(_ string, m []map[string]any) reply {
		switch {
		case phase.Load() == 1 && strings.Contains(allText(m), "needs attention"):
			phase.Store(2)
			return reply{calls: []event.ToolCall{tc("relay_screen", `{}`)}}
		case phase.Load() == 2:
			phase.Store(3)
			return r10Bad
		case phase.CompareAndSwap(3, 4):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"keys","keys":["1","Enter"]}`)}}
		}
		return hold()
	}, nil)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	phase.Store(1)
	s.seat.emit(relay.Event{Kind: relay.KindState, State: "blocked", Detail: "Do you trust this folder? 1. Yes 2. No"})
	keys := func() [][]string {
		s.seat.mu.Lock()
		defer s.seat.mu.Unlock()
		return append([][]string(nil), s.seat.keys...)
	}
	s.waitFor("the keys", func() bool { return len(keys()) > 0 })
	s.settled()
	if k := keys(); len(k) != 1 {
		t.Fatalf("keys sent: %q, want once", k)
	}
	if n := r10Runs(s, "relay_screen"); n != 1 {
		t.Fatalf("the screen was read %d times, want once", n)
	}
}

// r10LateReply runs a session on a 200k model through one restart, then
// moves eagent to a 60k model in settings while the first call after that
// restart is still out on the 200k one. That reply lands after the move,
// with a prompt of 38k, and answers the user. It returns the session and
// the 60k model.
func r10LateReply(t *testing.T) (*session, *scripted) {
	t.Helper()
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	var prompt atomic.Int64
	prompt.Store(120_000)
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var blockNext, out atomic.Bool
	s := startSession(t, "", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			prompt.Store(38_000)
			blockNext.Store(true)
			// A long briefing, as a 200k model's dossier is.
			return reply{text: briefing(p[max(0, len(p)-3):]) + "\n" + strings.Repeat("The work on the parser went through many steps. ", 2000)}
		}
		r := r7RestartLooks(m, int(prompt.Load()))
		if blockNext.CompareAndSwap(true, false) {
			out.Store(true)
			r.block = gate
		}
		return r
	}, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 200_000, 2048
	})
	t.Cleanup(release)
	r5LogOnFailure(s)
	s.settled()
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "first", Route: "eagent"}) })
	r6Until(t, "the first restart", func() bool { return r6Dossiers(s) == 1 })
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "second", Route: "eagent"}) })
	r6Until(t, "the first call after the restart to go out", out.Load)
	// The 60k model counts the conversation the 200k briefing opens as
	// 40k, past its restart size, until it is restarted for its window.
	var restarted atomic.Bool
	small := r5Swap(t, s, 60_000, func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			restarted.Store(true)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		if !restarted.Load() {
			return r7RestartLooks(m, 40_000)
		}
		return r7RestartLooks(m, 5000)
	})
	release()
	r6Until(t, "the late answer", func() bool { return r5Told(s, "Noted.") >= 2 })
	s.settled()
	return s, small
}

// The late reply's prompt was the 200k model's: it is not taken for the
// first prompt of the conversation on the 60k model. So when the user
// writes next, the conversation is restarted for 60k (a briefing sized for
// it), and the user is not told that no restart can make room.
func TestALateReplyFromTheOldModelIsNotTheNewOnesFirstPrompt(t *testing.T) {
	s, _ := r10LateReply(t)
	var first int
	s.rt.sync(func() { first = int(s.rt.turns.firstPrompt) })
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "third", Route: "eagent"}) })
	r6Until(t, "a restart on the 60k model, or the full notice", func() bool {
		return r6Dossiers(s) >= 2 || r6ToldFull(s) > 0
	})
	if n := r6ToldFull(s); n > 0 {
		t.Fatalf("told %d times that no restart can make room, after a late 200k reply set the first prompt to %d", n, first)
	}
}

// The same from the record: a session stopped after the late reply and
// resumed on the 60k model rebuilds the first prompt as the running one
// holds it, not from the 200k model's reply.
func TestALateReplyFromTheOldModelIsNotTheFirstPromptOnResume(t *testing.T) {
	s, small := r10LateReply(t)
	var first, calls int
	s.rt.sync(func() { first, calls = int(s.rt.turns.firstPrompt), s.rt.turns.subCalls })
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()
	useFakeRelays(t, newFakeSeat("unused"))
	cfg := testConfig(small.srv.URL)
	cfg.RolloverTokens = 0
	cfg.Model.Model, cfg.Model.ContextTokens, cfg.Model.MaxTokens = "other-model", 60_000, 2048
	rt, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 8)}, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range rt.st.Events {
		var d event.RouteData
		if ev.Type == event.Route && ev.Decode(&d) == nil && strings.HasPrefix(d.Reason, "the session was resumed") {
			t.Fatal("resumed on another model than the 60k one")
		}
	}
	if int(rt.turns.firstPrompt) == 38_000 || int(rt.turns.firstPrompt) != first || rt.turns.subCalls != calls {
		t.Fatalf("resumed with a first prompt of %d and %d calls; the running session had %d and %d, and the 200k reply was 38000", rt.turns.firstPrompt, rt.turns.subCalls, first, calls)
	}
}
