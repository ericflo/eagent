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
func TestWhenAnotherRestartIsWorthTrying(t *testing.T) {
	const (
		some, nothing, unknown = carriedSome, carriedNothing, carriedUnknown
	)
	for _, c := range []struct {
		carried carried
		refused bool
		since   bool
		retried bool
		decided bool
		fits    bool
		restart bool
	}{
		// Refused on its first call.
		{some, true, false, false, false, false, true},
		{some, true, true, false, false, false, true},
		{nothing, true, false, false, false, false, false}, // the opening alone was refused
		{nothing, true, true, false, false, false, true},   // the one more try
		{nothing, true, true, true, false, false, false},   // after it, stop
		{nothing, true, false, true, false, false, false},
		{unknown, true, false, false, false, false, true},
		{unknown, true, true, false, false, false, true},
		{some, true, true, false, true, false, false}, // decided stands
		{nothing, true, true, false, true, false, false},
		{unknown, true, true, false, true, false, false},
		// The first call went through, past the restart size.
		{some, false, false, false, false, true, true},
		{nothing, false, true, true, false, true, true},
		{some, false, true, false, false, false, false},
		{nothing, false, false, false, true, true, true},
	} {
		f := restartFacts{carried: c.carried, refused: c.refused, sinceOpening: c.since, retried: c.retried, decided: c.decided, openingFits: c.fits}
		if got := restartHelps(f); got != c.restart {
			t.Errorf("%+v: restart %v, want %v", f, got, c.restart)
		}
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
	r4Until(t, "the answer from the larger model", func() bool { return r5Told(s, "Here it is.") == 1 })
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
