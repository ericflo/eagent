package harness

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// A working agent records progress all the time, also while a dossier is
// written. A restart that carried nothing and was refused on its first
// call cannot be followed by one that does better, whatever was recorded
// meanwhile: the next restart carries nothing either, and opens on the
// same briefing.
func TestAgentProgressDuringEachPassDoesNotBuyAnotherRestart(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{5 * time.Millisecond})
	route := &r5PictureRoute{window: 60000, ceiling: 2048, picture: 80000}
	var s *session
	var ready atomic.Bool
	inner := route.brain()
	brain := func(model string, m []map[string]any) reply {
		if ready.Load() && dossierRequest(m) != "" {
			// The agent says something while the pass is out.
			s.rt.sync(func() {
				s.rt.append(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "codex", Text: "running the tests"}))
			})
		}
		return inner(model, m)
	}
	s = startSession(t, "", brain, func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	})
	r5LogOnFailure(s)
	s.settled()
	ready.Store(true)
	route.refuseAll.Store(true)
	r5SendPicture(s, "@e what does this screenshot show?")
	r4Until(t, "the user to be told no restart can make room, or a restart loop", func() bool {
		return r5Told(s, fullNotice) > 0 || len(s.events(event.Dossier)) > 6
	})
	// The first restart carries the picture; the second carries nothing,
	// and the progress recorded during its pass buys the one more try; the
	// third is the last.
	n := len(s.events(event.Dossier))
	t.Logf("%d restarts", n)
	if n > 3 {
		t.Fatalf("%d restarts, want at most 3: each refused restart carried nothing, and a progress line that came while the pass was written bought another", n)
	}
}

// Whether another restart is worth trying, for a conversation a restart
// began and that was too long from its start. since is whether the refused
// request held events after the briefing (a message written during the
// pass, the agent's progress); what kind they were does not matter, only
// that the next restart would leave them out. A route changed to another
// window is not a row: the next call goes to the new route as it is (a
// larger window takes what the old one refused, with no restart), and a
// restart sized for it would open on a briefing at least as long.
//
// alone is whether the opening by itself fits the window (refused rows);
// fits whether it leaves a message's room under the restart size (rows
// whose first call went through). rescues is how many restarts in a row
// before this conversation were made for conversations too long from
// their start; a conversation whose first call fits sets it back to 0.
// For each row that stops, cause is what the user is told was too large.
func TestWhenAnotherRestartIsWorthTrying(t *testing.T) {
	const (
		some, nothing, unknown = carriedSome, carriedNothing, carriedUnknown
		opening, burst, again  = fullOpening, fullBurst, fullAgain
	)
	for _, c := range []struct {
		carried carried
		refused bool
		since   bool
		retried bool
		rescues int
		alone   bool
		decided bool
		fits    bool
		restart bool
		cause   fullCause
	}{
		// Refused on its first call.
		{some, true, false, false, 0, true, false, false, true, 0},
		{some, true, true, false, 0, true, false, false, true, 0},
		{nothing, true, false, false, 0, true, false, false, false, opening}, // the opening alone was refused
		{nothing, true, true, false, 0, true, false, false, true, 0},         // the one more try
		{nothing, true, true, true, 1, true, false, false, false, burst},     // after it, stop
		{nothing, true, false, true, 1, true, false, false, false, opening},
		{unknown, true, false, false, 0, true, false, false, true, 0},
		{unknown, true, true, false, 0, true, false, false, true, 0},
		{some, true, true, false, 0, true, true, false, false, burst}, // decided stands
		{nothing, true, true, false, 0, true, true, false, false, burst},
		{unknown, true, true, false, 0, true, true, false, false, burst},
		// The opening by itself does not fit the window: nothing a
		// restart does can help, whatever arrived or was carried.
		{some, true, true, false, 0, false, false, false, false, opening},
		{nothing, true, true, false, 0, false, false, false, false, opening},
		{unknown, true, false, false, 0, false, false, false, false, opening},
		// Two rescues in a row already: the third restart was the last.
		{some, true, true, false, 2, true, false, false, false, burst},
		{unknown, true, false, false, 2, true, false, false, false, opening},
		// The first call went through, past the restart size.
		{some, false, false, false, 0, false, false, true, true, 0},
		{nothing, false, true, true, 0, false, false, true, true, 0},
		{nothing, false, false, false, 1, false, false, true, true, 0},
		{some, false, true, false, 0, false, false, false, false, opening}, // the opening leaves no room
		{nothing, false, false, false, 0, false, true, true, true, 0},
		{nothing, false, false, false, 2, false, false, true, false, again}, // three in a row, each past the size
		{nothing, false, false, false, 2, false, false, false, false, opening},
	} {
		f := restartFacts{carried: c.carried, refused: c.refused, sinceOpening: c.since, retried: c.retried, rescues: c.rescues, openingAloneFits: c.alone, decided: c.decided, openingFits: c.fits}
		got := restartHelps(f)
		if got != c.restart {
			t.Errorf("%+v: restart %v, want %v", f, got, c.restart)
		}
		if !got && causeOf(f) != c.cause {
			t.Errorf("%+v: the user would be told cause %d, want %d", f, causeOf(f), c.cause)
		}
	}
	// Refused after calls of this conversation went through (it grew to
	// the window): a restart, whatever was found before about restarts
	// made for size, up to one past the bound on restarts in a row. After "three times in a row" (rescues 2)
	// and after "the notes and instructions alone" when that was found on
	// a first call that went through (openingFits false), calls go
	// through and a later refusal is this row. When "the notes and
	// instructions alone" was found on a refused first call, no call of
	// that conversation ever goes through, so this row never follows it.
	for _, f := range []restartFacts{
		{refusedAfterCalls: true, rescues: 2},
		{refusedAfterCalls: true, rescues: 2, decided: true},
		{refusedAfterCalls: true, openingFits: false},
		{refusedAfterCalls: true, carried: carriedNothing, retried: true},
	} {
		if !restartHelps(f) {
			t.Errorf("%+v: no restart, though the conversation grew to the window", f)
		}
	}
	// Past one more than the bound, those restarts stop too, and the user is
	// told the conversation reached the window again.
	if f := (restartFacts{refusedAfterCalls: true, rescues: restartRescueMax + 1}); restartHelps(f) || causeOf(f) != fullGrew {
		t.Errorf("%+v: restarted, or told cause %d, after the restarts in a row were spent", f, causeOf(f))
	}
}

// r5FullSession runs a session on a small route that refuses everything but
// the dossier pass, until the user is told no restart can make room.
func r5FullSession(t *testing.T) (*session, *r5PictureRoute) {
	setFor(t, &errorRetryDelays, []time.Duration{5 * time.Millisecond})
	route := &r5PictureRoute{window: 60000, ceiling: 2048, picture: 80000}
	s := startSession(t, "", route.brain(), func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	})
	r5LogOnFailure(s)
	s.settled()
	route.refuseAll.Store(true)
	r5SendPicture(s, "@e what does this screenshot show?")
	r4Until(t, "the user to be told no restart can make room", func() bool { return r5Told(s, fullNotice) > 0 })
	return s, route
}

// r5Swap moves eagent's calls to a model of the given window served by
// brain, as a pick in the thread's settings does.
func r5Swap(t *testing.T, s *session, window int, brain func(string, []map[string]any) reply) *scripted {
	srv := newScripted(brain)
	t.Cleanup(srv.srv.Close)
	conf := config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: srv.srv.URL, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: window, MaxTokens: 2048}
	eps, err := conf.Routes()
	if err != nil {
		t.Fatal(err)
	}
	s.rt.sync(func() {
		s.rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: "other-model"}, eps: eps, conf: conf})
	})
	return srv
}

// After no restart could make room, the person picks a model with a larger
// window: the conversation the old one refused goes to it as it is, and
// their message is answered, with no restart.
func TestAModelWithALargerWindowTakesWhatNoRestartCouldFit(t *testing.T) {
	s, _ := r5FullSession(t)
	before := len(s.events(event.Dossier))
	r5Swap(t, s, 200_000, func(_ string, m []map[string]any) reply {
		if strings.Contains(lastUserText(m), "kept for you") && m[len(m)-1]["role"] != "tool" {
			return say("Here it is.")
		}
		return hold()
	})
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"}) })
	// The look the larger model is sent first may answer the message that
	// waited since before the switch, this one, or both at once.
	r4Until(t, "the answer from the larger model", func() bool { return r5Told(s, "Here it is.") >= 1 })
	s.waitFor("the loop to take in both messages", func() bool {
		idle := false
		s.rt.sync(func() { idle = !s.rt.turns.busy && !s.rt.wakePending() })
		return idle
	})
	if n := len(s.events(event.Dossier)); n != before {
		t.Fatalf("%d restarts on the larger model, want none", n-before)
	}
}

// A model with a smaller window refuses the same opening: nothing changes,
// no restart is made and the full notice is not said again. The person who
// writes is told once that their message waits, naming it.
func TestAModelWithASmallerWindowStaysWhereNoRestartCanMakeRoom(t *testing.T) {
	s, _ := r5FullSession(t)
	before := len(s.events(event.Dossier))
	small := r5Swap(t, s, 30_000, func(string, []map[string]any) reply {
		return reply{status: 400, body: `{"error":{"message":"prompt is too long for the model's context window","type":"invalid_request_error"}}`}
	})
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"}) })
	r4Until(t, "refused calls on the smaller model", func() bool { return small.count("other-model") > 20 })
	if n := len(s.events(event.Dossier)); n != before {
		t.Fatalf("%d restarts on the smaller model, want none", n-before)
	}
	if n := r5Told(s, fullNotice); n != 1 {
		t.Fatalf("the full notice was said %d times, want once", n)
	}
	if n := r5Told(s, stillFullNotice+`"still there?"`); n != 1 {
		t.Fatalf("the user was told %d times that their message waits, want once", n)
	}
}

// Each message written after the user was told no restart can make room is
// answered by the harness once, naming it: neither silence nor the same
// notice again.
func TestEachMessageAfterNoRestartCanMakeRoomIsToldItWaits(t *testing.T) {
	s, _ := r5FullSession(t)
	for _, text := range []string{"any news?", "hello?"} {
		calls := s.model.count(testModel)
		s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: text, Route: "eagent"}) })
		r4Until(t, "refused calls", func() bool { return s.model.count(testModel) > calls+10 })
		if n := r5Told(s, stillFullNotice+fmt.Sprintf("%q", text)); n != 1 {
			t.Fatalf("the user was told %d times that %q waits, want once", n, text)
		}
	}
	if n := r5Told(s, stillFullNotice); n != 2 {
		t.Fatalf("%d notices that a message waits, want one for each of the two", n)
	}
	if n := r5Told(s, fullNotice); n != 1 {
		t.Fatalf("the full notice was said %d times, want once", n)
	}
}

// Right after a switch to a model with a smaller window, before any call
// has gone to it, the conversation is past the new model's restart size.
// That door leads to the same decision: no restart can make room. With a
// larger window, the decision no longer stands and the size of the new
// window is asked as usual.
func TestNoRestartCanMakeRoomIsAskedAtTheSizeDoorAfterASwitch(t *testing.T) {
	refuse := func(string, []map[string]any) reply {
		return reply{status: 400, body: `{"error":{"message":"prompt is too long for the model's context window","type":"invalid_request_error"}}`}
	}
	for _, c := range []struct {
		window  int
		decided bool
	}{{30_000, true}, {60_000, true}, {200_000, false}} {
		s, _ := r5FullSession(t)
		// The refused calls are retried after an hour from here on (set on
		// the loop, which reads it), so none is out when the switch comes.
		s.rt.sync(func() { errorRetryDelays = []time.Duration{time.Hour} })
		r4Until(t, "the full notice decided and no call out", func() bool {
			ok := false
			s.rt.sync(func() { ok = s.rt.turns.futileLogged && !s.rt.turns.busy && s.rt.turns.failures > 0 })
			return ok
		})
		srv := newScripted(refuse)
		t.Cleanup(srv.srv.Close)
		conf := config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: srv.srv.URL, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: c.window, MaxTokens: 2048}
		eps, err := conf.Routes()
		if err != nil {
			t.Fatal(err)
		}
		var decided, needs bool
		var prompt, size int
		s.rt.sync(func() {
			// The switch, and the loop's next look at the size, at once:
			// before any call has gone to the new model.
			s.rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: "other-model"}, eps: eps, conf: conf})
			decided = s.rt.restartDecided()
			needs = s.rt.needsDossier()
			prompt, size = int(s.rt.contextTokens()), int(s.rt.rolloverTokens())
		})
		if c.decided && prompt < size {
			t.Fatalf("window %d: the prompt (%d) is under the new restart size (%d), so the size door is not tested", c.window, prompt, size)
		}
		if decided != c.decided {
			t.Errorf("window %d: decided %v, want %v", c.window, decided, c.decided)
		}
		if c.decided && needs {
			t.Errorf("window %d: a restart is asked for right after the switch, and no restart can make room", c.window)
		}
		s.stop()
	}
}
