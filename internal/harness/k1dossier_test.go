package harness

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// k1BadRequest is a failure that is not a refusal as too long, and that
// the client does not retry on its own.
var k1BadRequest = reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}

// Every turn's first call goes through (it makes a lookup) and the call
// after it fails, not as too long. The turn that failed is owed again, so
// the next one starts; its first call coming back is no sign that the
// turn's calls go through, so the waits between those turns grow as for
// any failing call, where each turn once started the next at once, paid
// for its first call, and said it was trying again now, without end.
func TestK1ASecondCallThatFailsAfterTheFirstWentThroughWaitsLongerEachTime(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	var ready atomic.Bool
	brain := func(_ string, m []map[string]any) reply {
		switch {
		case !ready.Load():
			return hold()
		case m[len(m)-1]["role"] == "tool":
			return k1BadRequest
		}
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
	}
	s := startSession(t, "", brain, nil)
	r5LogOnFailure(s)
	s.settled()
	ready.Store(true)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what is the status?", Route: "eagent"}) })
	r6Until(t, "a wait of an hour, or calls going on without one", func() bool {
		return r4Logged(s, "trying again in") > 0 || r4Logged(s, "model call failed") > 30
	})
	if n := r4Logged(s, "trying again in"); n != 1 {
		t.Fatalf("a second call that failed after the first went through was tried again at once each time, the waits never growing (%d failed calls logged)", r4Logged(s, "model call failed"))
	}
	if n := r4Logged(s, "model call failed"); n != 2 {
		t.Fatalf("%d failed calls logged before the wait of an hour, want 2", n)
	}
}

// A session with no terminal whose restart could not be made (the file
// for the next conversation could not be created) has its prompt refused
// as too long twice more: two failures in a row. Then a turn of one call
// runs to its end: the model answers. That turn shows the model can be
// reached, so the session ends with its closing word, not as one whose
// model is down (eagent's own closing words, exit 2).
func TestK1ABatchSessionWhoseTurnEndedWellAfterRefusalsIsNotEndedAsDown(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made read-only for root")
	}
	setFor(t, &errorRetryDelays, []time.Duration{0, 0, time.Hour})
	t.Setenv("EAGENT_TEST_KEY", "x")
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	var writing atomic.Bool
	var calls atomic.Int32
	model := newScripted(func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			writing.Store(true)
			p := places(ask)
			return reply{block: gate, text: briefing(p[max(0, len(p)-3):])}
		}
		// The first turn stops the agent, and the call after that is
		// refused as too long: a restart, which cannot be made. Two more
		// refusals, then a turn of one call that answers.
		switch n := calls.Add(1); {
		case n == 1:
			return stopsAndReports("Stopping Codex; checking the status.")
		case n <= 4:
			return r7TooLong
		case m[len(m)-1]["role"] == "tool":
			return hold()
		}
		return say("The status is green.")
	})
	t.Cleanup(model.srv.Close)
	useFakeRelays(t, newFakeSeat("ok"))
	ui := &fakeUI{}
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: "@e what is the status?"}, ui)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- rt.Run(context.Background()) }()
	waitFor(t, "the dossier call to go out", writing.Load)
	if err := os.Chmod(rt.sess.Path, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(rt.sess.Path, 0o755) })
	release()
	var code int
	select {
	case code = <-done:
	case <-time.After(15 * time.Second):
		rt.stop()
		<-done
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	failed, gaveUp := 0, 0
	for _, l := range ui.logged() {
		if strings.Contains(l, "model call failed") {
			failed++
		}
		if strings.Contains(l, "the conversation continues as it is for now") {
			gaveUp++
		}
	}
	if failed != 2 || gaveUp != 1 {
		t.Fatalf("%d failed calls and %d restarts given up, want 2 and 1", failed, gaveUp)
	}
	for _, m := range ui.said() {
		if strings.HasPrefix(m, "This session is ending and I could not write a proper closing report") {
			t.Fatalf("a session whose last turn ended well was closed as one whose model is down: %q", m)
		}
	}
	if code != 0 {
		t.Fatalf("exit %d after a turn that ended well, want 0; said %q", code, ui.said())
	}
}

// k1Swap moves eagent's calls to another model with this window and reply
// ceiling, served by brain, as a pick in the thread's settings does.
func k1Swap(t *testing.T, s *session, window, ceiling int, brain func(string, []map[string]any) reply) *scripted {
	srv := newScripted(brain)
	t.Cleanup(srv.srv.Close)
	conf := config.ModelRoute{Protocol: llm.ProtocolChat, BaseURL: srv.srv.URL, Model: "other-model", APIKeyEnv: "EAGENT_TEST_KEY", ContextTokens: window, MaxTokens: ceiling}
	eps, err := conf.Routes()
	if err != nil {
		t.Fatal(err)
	}
	s.rt.sync(func() {
		s.rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: "other-model"}, eps: eps, conf: conf})
	})
	return srv
}

// The burst of pictures keeps coming during both passes on the first
// model, so the one more try is spent and no restart can make room there.
// The person then picks a model with the same room: the restarts in a row
// and the one more try were the old model's (newModel), so the new one
// gets its own restart, which sets the burst aside by place, and the
// person's message is answered. The decision found on the old model does
// not stand on it, as one found on the opening alone would.
func TestK1ABurstDecisionDoesNotStandOnAModelPickedAfterIt(t *testing.T) {
	const window, ceiling = 128_000, 16384
	t.Setenv("EAGENT_TEST_KEY", "x")
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	s, _ := r6QuietRestart(t, window, func(s *session, pass int) {
		if pass <= 2 {
			r6SendPictures(s, window)
		}
	})
	if n := r5Told(s, fullNotice+": while I was rereading"); n != 1 {
		t.Fatalf("told %d times that what arrived during the passes is too much, want once", n)
	}
	before := r6Dossiers(s)
	k1Swap(t, s, window, ceiling, r6Brain(window, ceiling, 100))
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "@e still there?", Route: "eagent"}) })
	r6Until(t, "an answer, or the message told it waits", func() bool {
		return r5Told(s, "Noted.") > 1 || r5Told(s, stillFullNotice+`"@e still there?"`) > 0 || r5Told(s, stillFullNotice+`"still there?"`) > 0
	})
	if r5Told(s, "Noted.") < 2 {
		t.Fatalf("on the model picked after the decision: %d restarts, and the message was told it waits; that model's own restart sets the burst aside", r6Dossiers(s)-before)
	}
	if n := r6Dossiers(s) - before; n != 1 {
		t.Fatalf("%d restarts on the model picked after the decision, want its one", n)
	}
}

// Pictures arrive during every pass, so each new conversation's first
// call is past the restart size, inside the window: after the restart
// that started the run and two more in a row, the restarts stop and the
// user is told. The session is stopped twice while the first call after a
// restart is out, and resumed: the count of restarts in a row is the
// record's, so the bound is the same as without the stops (once counted
// in memory, each stop started it again and bought two more restarts).
func TestK1RestartsInARowCountOnAcrossAResume(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	const window, ceiling, stops = 200_000, 16384, 2
	inner := r6Brain(window, ceiling, 100)
	var cur atomic.Pointer[session]
	var mu sync.Mutex
	armed := false // a pass has just landed: the next look is its conversation's first
	passes := 0    // dossier passes seen
	stopAt := 0    // the pass whose conversation's first call is held (0: none)
	var holdCh chan struct{}
	out := make(chan struct{}, 8)
	var held []chan struct{}
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			select {
			case <-c:
			default:
				close(c)
			}
		}
	})
	brain := func(model string, m []map[string]any) reply {
		if dossierRequest(m) != "" {
			mu.Lock()
			passes++
			n := passes
			mu.Unlock()
			if s := cur.Load(); s != nil {
				r6SendEach(s, 1, func(int) event.UserMessageData {
					return r6WithPictures(fmt.Sprintf("@e more screenshots, set %d", n), 7, s)
				})
			}
			r := inner(model, m)
			mu.Lock()
			armed = true
			mu.Unlock()
			return r
		}
		mu.Lock()
		hold := armed && stopAt > 0 && passes == stopAt
		armed = false
		var ch chan struct{}
		if hold {
			stopAt = 0
			ch = make(chan struct{})
			held = append(held, ch)
			holdCh = ch
		}
		mu.Unlock()
		if hold {
			out <- struct{}{}
			<-ch
		}
		return inner(model, m)
	}
	tune := func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = window, ceiling
	}
	s := startSession(t, "", brain, tune)
	cur.Store(s)
	r5LogOnFailure(s)
	r4Settled(s)
	limit := 0
	s.rt.sync(func() { limit = int(s.rt.rolloverTokens()) })
	// Hold the first call of the conversation the second restart opens.
	mu.Lock()
	stopAt = 2
	mu.Unlock()
	r6SendEach(s, 1, func(int) event.UserMessageData {
		return r6WithPictures("@e hello, here is how it looks", limit/r6PictureTokens+1, s)
	})
	for i := 0; i < stops; i++ {
		select {
		case <-out:
		case <-time.After(10 * time.Second):
			t.Fatalf("run %d: the first call after a restart never went out (%d restarts, told %d)", i+1, len(s.events(event.Dossier)), r6ToldFull(s))
		}
		if r6ToldFull(s) > 0 {
			t.Fatalf("run %d: told before the stop", i+1)
		}
		cfg, path, project := s.rt.cfg, s.rt.sess.Path, s.rt.opts.Project
		cur.Store(nil)
		s.stop()
		mu.Lock()
		if i+1 < stops {
			stopAt = passes + 1
		}
		close(holdCh)
		mu.Unlock()
		s = audit2Resume(t, cfg, Options{Project: project, Interactive: true}, path, s.seat, s.model)
		cur.Store(s)
		r5LogOnFailure(s)
	}
	r6Until(t, "the user told the restarts stop", func() bool { return r6ToldFull(s) > 0 || len(s.events(event.Dossier)) > 8 })
	if n := len(s.events(event.Dossier)); n != 3 {
		t.Fatalf("%d restarts in a row before the user was told, want 3, as without the %d stops", n, stops)
	}
	if told := r6ToldFull(s); told != 1 {
		t.Fatalf("the user was told %d times, want once", told)
	}
}

// As in TestR14NoRestartAtTheWindowIsPromisedWhenNoneWillBeMade, the
// restarts stop one past the bound, and the user is told that none can be
// made when the conversation reaches the window. The session is stopped
// and resumed, and then the window is reached: the resumed run counts the
// restarts in a row from the record, so it makes none there either, and
// what the user was told holds.
func TestK1NoRestartAtTheWindowIsMadeAfterAResumeWhenNoneWasPromised(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	const (
		holding = iota
		past    // every call is past the restart size
		lookup  // a lookup, and the call after it refused as too long
	)
	var phase atomic.Int32
	var limit, refusals atomic.Int64
	var cur atomic.Pointer[session]
	send := func(i int) {
		s := cur.Load()
		d := r6WithPictures(fmt.Sprintf("status %d?", i), 1, s)
		s.rt.sync(func() { s.rt.userMessage(d) })
	}
	brain := func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch phase.Load() {
		case past:
			r := say("Noted.")
			r.prompt = int(limit.Load()) + 5000
			return r
		case lookup:
			if m[len(m)-1]["role"] == "tool" {
				if refusals.Add(1) == 1 {
					send(5)
					phase.Store(past)
				}
				return r7TooLong
			}
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"tests"}`)}}
		}
		return hold()
	}
	tune := func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = 200_000, 2048
	}
	s := startSession(t, "", brain, tune)
	cur.Store(s)
	r5LogOnFailure(s)
	s.settled()
	s.rt.sync(func() { limit.Store(int64(s.rt.rolloverTokens())) })
	phase.Store(past)
	for i := 1; i <= 3; i++ {
		send(i)
		r6Until(t, fmt.Sprintf("restart %d", i), func() bool { return r6Dossiers(s) >= i })
	}
	s.settled()
	phase.Store(lookup)
	send(4)
	r6Until(t, "the user told the restarts stop", func() bool { return r6ToldFull(s) > 0 || r6Dossiers(s) > 4 })
	var notice string
	for _, m := range s.ui.said() {
		if strings.HasPrefix(m, fullNotice) {
			notice = m
		}
	}
	if !strings.Contains(notice, "I can't restart it again") {
		t.Fatalf("setup: %d restarts, and the user was told %q", r6Dossiers(s), notice)
	}
	s.settled()
	before := len(s.events(event.Dossier))
	cfg, path, project := s.rt.cfg, s.rt.sess.Path, s.rt.opts.Project
	s.stop()
	s = audit2Resume(t, cfg, Options{Project: project, Interactive: true}, path, s.seat, s.model)
	cur.Store(s)
	r5LogOnFailure(s)
	s.settled()
	// The conversation reaches the window.
	phase.Store(lookup)
	send(6)
	r6Until(t, "a restart, or the user told the model refuses the conversation", func() bool {
		return r6Dossiers(s) > 0 || r5Told(s, "My model refuses my conversation as too long") > 0
	})
	if n := len(s.events(event.Dossier)) - before; n != 0 {
		t.Fatalf("the resumed run made %d restarts at the window, and the user was told none can be made there: %q", n, notice)
	}
}

// As in r6BurstDuringBothPasses, a burst of pictures arrives during both
// passes, so the second restart is the one more try, and its first call is
// refused too: no restart can make room. Here the session is stopped while
// that call is out, and resumed. The resumed run reads from the record
// that the restart carried nothing and was the one more try, so it stops
// the restarts and says so, as the run that was stopped would have; it
// does not buy a third.
func TestK1TheOneMoreTryIsTheLastAcrossAResume(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{time.Millisecond})
	const window, ceiling = 128_000, 16384
	inner := r6Brain(window, ceiling, 100)
	var cur atomic.Pointer[session]
	var passes, after atomic.Int32
	out := make(chan struct{}, 1)
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	brain := func(model string, m []map[string]any) reply {
		if dossierRequest(m) != "" {
			if n := passes.Add(1); n <= 2 {
				if s := cur.Load(); s != nil {
					r6SendPictures(s, window)
				}
			}
			return inner(model, m)
		}
		if passes.Load() == 2 && after.Add(1) == 1 {
			// The first call after the one more try: held while the
			// session is stopped.
			out <- struct{}{}
			<-gate
		}
		return inner(model, m)
	}
	tune := func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = window, ceiling
	}
	s := startSession(t, "", brain, tune)
	cur.Store(s)
	r5LogOnFailure(s)
	r4Settled(s)
	limit := 0
	s.rt.sync(func() { limit = int(s.rt.rolloverTokens()) })
	r6SendEach(s, 1, func(int) event.UserMessageData {
		return r6WithPictures("@e hello, here is how it looks", limit/r6PictureTokens+1, s)
	})
	select {
	case <-out:
	case <-time.After(10 * time.Second):
		t.Fatalf("the first call after the second restart never went out (%d restarts)", len(s.events(event.Dossier)))
	}
	if n := len(s.events(event.Dossier)); n != 2 || r6ToldFull(s) != 0 {
		t.Fatalf("setup: %d restarts and told %d times before the stop, want 2 and none", n, r6ToldFull(s))
	}
	cfg, path, project := s.rt.cfg, s.rt.sess.Path, s.rt.opts.Project
	cur.Store(nil)
	s.stop()
	release()
	s = audit2Resume(t, cfg, Options{Project: project, Interactive: true}, path, s.seat, s.model)
	r5LogOnFailure(s)
	r6Until(t, "the user told, or another restart", func() bool { return r6ToldFull(s) > 0 || r6Dossiers(s) > 0 })
	if n := len(s.events(event.Dossier)); n != 2 {
		t.Fatalf("%d restarts in all, want the 2 the run that was stopped would have made", n)
	}
	if n := r5Told(s, burstNotice); n != 1 {
		t.Fatalf("told %d times after the resume that what arrived during the passes is too much, want once", n)
	}
}

// No restart could make room on a model whose reply ceiling takes a large
// part of its window. The session is stopped, its config is edited to a
// model with the same window and a smaller ceiling (more room for the
// conversation), and it is resumed. As when that model is picked in the
// thread's settings (TestAModelWithMoreRoomForInputGetsItsOwnRestart), the
// decision found on less room does not stand on it: one restart on it
// makes room, and the message is answered.
func TestK1ADecisionFoundOnLessRoomDoesNotStandAfterAResumeOnMore(t *testing.T) {
	t.Skip("known, not fixed: the record does not hold the room a decision that no restart can make room was found on, so a run resumed on a model with more room keeps it (C2)")
	setFor(t, &errorRetryDelays, []time.Duration{5 * time.Millisecond})
	route := &r5PictureRoute{window: 60000, ceiling: 2048, picture: 80000}
	var restarted atomic.Bool
	other := newScripted(func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			restarted.Store(true)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case !restarted.Load():
			return r7TooLong
		case m[len(m)-1]["role"] != "tool":
			return say("Here it is.")
		}
		return hold()
	})
	t.Cleanup(other.srv.Close)
	s := startSession(t, "", route.brain(), func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	})
	r5LogOnFailure(s)
	s.settled()
	route.refuseAll.Store(true)
	r5SendPicture(s, "@e what does this screenshot show?")
	r4Until(t, "the user to be told no restart can make room", func() bool { return r5Told(s, fullNotice) > 0 })
	cfg, path, project := s.rt.cfg, s.rt.sess.Path, s.rt.opts.Project
	s.stop()
	cfg.Model.BaseURL, cfg.Model.Model, cfg.Model.MaxTokens = other.srv.URL, "other-model", 1024
	s2 := audit2Resume(t, cfg, Options{Project: project, Interactive: true}, path, s.seat, other)
	r5LogOnFailure(s2)
	s2.rt.sync(func() { s2.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"}) })
	r4Until(t, "an answer, or the message told it waits", func() bool {
		return r5Told(s2, "Here it is.") > 0 || r5Told(s2, stillFullNotice+`"still there?"`) > 0
	})
	if r5Told(s2, "Here it is.") == 0 {
		t.Fatalf("resumed on more room for the conversation: %d restarts, and the user was told their message waits; one restart there makes room", r6Dossiers(s2))
	}
}

// k1DownOnThePhone runs a session with no terminal that asks on the phone
// and stops its agent, and whose model then fails twice running (the user
// is told it cannot be reached): the question keeps the session open.
// stall, when set, is what the second failed call waits on before it
// fails (second says it is out). The model answers again from the moment
// healthy is set; answered says a call has been shown the user's answer.
func k1DownOnThePhone(t *testing.T, stall <-chan struct{}) (g *phoneRig, fp *phoneFake, healthy, answered *atomic.Bool, second chan struct{}) {
	setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
	fp = newPhoneFake(true)
	t.Cleanup(fp.srv.Close)
	healthy, answered = new(atomic.Bool), new(atomic.Bool)
	second = make(chan struct{}, 1)
	var asked atomic.Bool
	var failed atomic.Int32
	g = phoneStart(t, fp, "paint it", false, func(_ string, msgs []map[string]any) reply {
		all, tail := allText(msgs), lastUserText(msgs)
		if asked.Load() && !healthy.Load() {
			if failed.Add(1) == 2 && stall != nil {
				second <- struct{}{}
				return reply{block: stall, status: k1BadRequest.status, body: k1BadRequest.body}
			}
			return k1BadRequest
		}
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			if strings.Contains(all, "Blue it is") {
				return hold()
			}
			return say("Blue it is; done.")
		case strings.Contains(tail, "The user answered your question"):
			answered.Store(true)
			return say("Blue it is.")
		case strings.Contains(tail, "finished a turn") && !strings.Contains(all, "Which colour?"):
			asked.Store(true)
			return phoneAskAndStop("Which colour?", "red", "blue")
		}
		return hold()
	}, nil)
	return g, fp, healthy, answered, second
}

// After the model has failed twice running, it answers again, and the
// user answers the question on the phone. The answer clears the question,
// so nothing else keeps the session open, and the loop waits out the
// settle window before it looks: the session stays open for that look,
// the model is shown the answer, and the session ends with its closing
// word (exit 0), not in eagent's own closing words as one whose model is
// down (exit 2), with the answer read by nobody.
func TestK1AnAnswerAfterTheModelWasDownIsLookedAtBeforeTheEnd(t *testing.T) {
	setFor(t, &wakeSettle, 100*time.Millisecond)
	g, fp, healthy, answered, _ := k1DownOnThePhone(t, nil)
	waitFor(t, "the user told the model cannot be reached", func() bool {
		for _, m := range g.ui.said() {
			if strings.HasPrefix(m, "I can't reach my own model") {
				return true
			}
		}
		return false
	})
	g.agentStopped()
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	healthy.Store(true)
	if !fp.answer("blue", "") {
		t.Fatal("no pending question to answer")
	}
	code := g.exit(15 * time.Second)
	if !answered.Load() {
		t.Fatalf("the model was never shown the answer (exit %d); said %q", code, g.ui.said())
	}
	if code != 0 {
		t.Fatalf("exit %d after the answer was taken up, want 0; said %q", code, g.ui.said())
	}
}

// The same with no settle window, the answer arriving while the second
// failing call is out: it came after what that call was shown, so it is
// not held back by the wait after the failure, and is looked at first.
func TestK1AnAnswerThatArrivesDuringAFailingCallIsNotHeldBehindItsWait(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	g, fp, healthy, answered, second := k1DownOnThePhone(t, gate)
	select {
	case <-second:
	case <-time.After(10 * time.Second):
		t.Fatal("the second failing call never went out")
	}
	g.agentStopped()
	waitFor(t, "the question on the phone", func() bool { return fp.pendingQuestions() == 1 })
	if !fp.answer("blue", "") {
		t.Fatal("no pending question to answer")
	}
	waitFor(t, "the answer on the record", func() bool {
		n := 0
		g.rt.sync(func() {
			for _, ev := range g.rt.st.Events {
				if ev.Type == event.UserAnswer {
					n++
				}
			}
		})
		return n > 0
	})
	healthy.Store(true)
	release()
	code := g.exit(15 * time.Second)
	if !answered.Load() {
		t.Fatalf("the model was never shown the answer (exit %d); said %q", code, g.ui.said())
	}
	if code != 0 {
		t.Fatalf("exit %d after the answer was taken up, want 0; said %q", code, g.ui.said())
	}
}
