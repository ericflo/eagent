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
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// The same model at the same address written with a trailing slash is
// the route eagent is on, both to the loop (sameRoute: the model keeps
// its failures, restarts in a row and token ratio) and to the record (no
// move: the dossier's restarts in a row and the size of the first call
// stand), so a resumed run reads it as the running one did.
func TestK2AnAddressWithATrailingSlashIsNoMove(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	var before, after int64
	s.rt.sync(func() { before = s.rt.st.Moved() })
	conf := s.rt.cfg.Model
	old := conf.BaseURL
	conf.BaseURL += "/"
	eps, err := conf.Routes()
	if err != nil {
		t.Fatal(err)
	}
	moved, on := true, false
	s.rt.sync(func() {
		moved = !sameRoute(s.rt.models[event.ActorEagent].client.Endpoint, eps[0])
		s.rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: conf.Model}, eps: eps, conf: conf})
		after = s.rt.st.Moved()
		// A reply still out from before, from the address as it was
		// written then, came from the route in use.
		on = s.rt.st.OnRoute(event.ActorEagent, old, conf.Model)
	})
	if moved {
		t.Fatalf("setup: the loop takes %q for another address", eps[0].BaseURL)
	}
	if after != before {
		t.Fatalf("the record took %q for a move (seq %d), the loop for the same route", eps[0].BaseURL, after)
	}
	if !on {
		t.Fatalf("a reply from %q is not taken for the route in use, now written %q", old, eps[0].BaseURL)
	}
}

// C5: a session with no terminal stops its agent and reports; its closing
// look puts the conversation past the restart size. The session is ending,
// and nothing will be asked of that conversation again: no dossier pass is
// paid for before it ends.
func TestK2AnEndingBatchSessionPaysForNoDossier(t *testing.T) {
	var passes atomic.Int32
	_, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			passes.Add(1)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case m[len(m)-1]["role"] == "tool":
			return hold()
		case strings.Contains(lastUserText(m), "The session is ending now"):
			r := hold()
			r.prompt = 30000 // past the 20,000 restart size of testConfig
			return r
		}
		return stopsAndReports("The build is green; stopping Codex.")
	})
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	if code != 0 {
		t.Fatalf("exit %d; said %q", code, ui.said())
	}
	if n := passes.Load(); n != 0 {
		t.Fatalf("%d dossier passes paid for by a session that was ending", n)
	}
}

// C6: a dossier pass looks something up, and the call with what it read
// back is refused as too long. The results are taken out again and the
// writer is told to write without them: in the request that follows, that
// note is added to the request for the briefing (withNote), as the pass's
// other notes are, not a user message of its own right after it. (The
// conversation before the request is the record as rendered, which has
// user messages in a row of its own.)
func TestK2ADossierPassThatDropsItsLookupsSendsNoTwoUserMessagesInARow(t *testing.T) {
	var ready, written atomic.Bool
	var after atomic.Value // the last message of the request after the drop
	brain := func(_ string, m []map[string]any) reply {
		if dossierRequest(m) != "" {
			last := m[len(m)-1]
			text, _ := last["content"].(string)
			switch {
			case last["role"] == "tool":
				return r7TooLong
			case strings.Contains(allText(m), "did not fit in the model's context window") || written.Load():
				if !written.Load() {
					after.Store(text)
				}
				written.Store(true)
				p := places(dossierRequest(m))
				return reply{text: briefing(p[max(0, len(p)-3):])}
			}
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
		}
		r := hold()
		if ready.Load() {
			r.prompt = 30000 // past the 20,000 restart size of testConfig
		}
		return r
	}
	s := startSession(t, "", brain, nil)
	r5LogOnFailure(s)
	s.settled()
	ready.Store(true)
	r14AgentTurn(s, "The build is green.")
	r6Until(t, "the briefing written after the drop", written.Load)
	last, _ := after.Load().(string)
	if !strings.Contains(last, dossierAsk) || !strings.Contains(last, "did not fit in the model's context window") {
		t.Fatalf("the note after the drop is not in the request for the briefing; the request's last message is %.300q", last)
	}
}

// C7: the agent is stopped, then eagent's model fails twice running. The
// user is told so, and the notice does not say their messages still go
// straight to an agent: none is running, and what they write waits for
// eagent.
func TestK2TheDownNoticeWithNoAgentRunningSaysWhereMessagesGo(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer bool // the failing turns' first call comes back
	}{{"unreachable", false}, {"failing", true}} {
		t.Run(c.name, func(t *testing.T) {
			setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
			var stopped, failing atomic.Bool
			brain := func(_ string, m []map[string]any) reply {
				switch {
				case !stopped.Load():
					stopped.Store(true)
					return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"the work is done"}`)}}
				case !failing.Load():
					return hold()
				case c.answer && m[len(m)-1]["role"] != "tool":
					return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
				}
				return k1BadRequest
			}
			s := startSession(t, "", brain, nil)
			r5LogOnFailure(s)
			s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "@e stop codex", Route: "eagent"}) })
			r6Until(t, "the agent stopped", func() bool {
				n := 1
				s.rt.sync(func() { n = len(s.rt.st.ActiveRelays()) })
				return n == 0
			})
			s.settled()
			failing.Store(true)
			s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what now?", Route: "eagent"}) })
			r6Until(t, "the notice", func() bool { return r4Logged(s, "trying again in") > 0 })
			for _, m := range s.ui.said() {
				if strings.Contains(m, "can't reach my own model") || strings.HasPrefix(m, failingNotice) {
					if strings.Contains(m, "straight to") {
						t.Fatalf("with no agent running, told: %q", m)
					}
					return
				}
			}
			t.Fatalf("no notice; said %q", s.ui.said())
		})
	}
}

// C11: the session's record can no longer be written, and a message for
// the agent (or a command) comes in through the inbox. The session ends on
// the failure, but the message is not taken off its source before it is on
// the record, where once it was removed and lost. A file written into the
// inbox (by another process, or a script) stays for the next run. Words a
// web page hands to this run (Take) are refused instead, with the file
// removed, so the page keeps them and nothing waits to be taken twice.
func TestK2AnInboxMessageIsNotTakenOffItsSourceWhenTheRecordFails(t *testing.T) {
	inboxFiles := func(path string) int {
		entries, _ := os.ReadDir(InboxDir(path))
		n := 0
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				n++
			}
		}
		return n
	}
	for _, c := range []struct{ name, text string }{
		{"words", "run the tests"},
		{"a command", "/restart"},
	} {
		t.Run(c.name+"/a file in the inbox", func(t *testing.T) {
			s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
			s.settled()
			path := s.rt.sess.Path
			s.rt.sync(func() { _ = s.rt.sess.Close() })
			if err := PostInbox(path, InboxMessage{Type: "message", Text: c.text, From: "web", Route: "agent"}); err != nil {
				t.Fatal(err)
			}
			s.rt.sync(func() { s.rt.pollInbox() })
			if n := inboxFiles(path); n != 1 {
				t.Fatalf("%d files left in the inbox after the record failed, want the message's one", n)
			}
			if r4Logged(s, "FATAL: cannot append") == 0 {
				t.Fatalf("setup: the record did not fail; logs %q", s.ui.logged())
			}
		})
		t.Run(c.name+"/handed to this run", func(t *testing.T) {
			s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
			s.settled()
			path := s.rt.sess.Path
			s.rt.sync(func() { _ = s.rt.sess.Close() })
			took, err := s.rt.Take(InboxMessage{Type: "message", Text: c.text, From: "web", Route: "agent"})
			if took || err == nil {
				t.Fatalf("Take = %v, %v; want it refused, so the page keeps the words", took, err)
			}
			if n := inboxFiles(path); n != 0 {
				t.Fatalf("%d files left in the inbox though the page keeps the words, want none", n)
			}
			if r4Logged(s, "FATAL: cannot append") == 0 {
				t.Fatalf("setup: the record did not fail; logs %q", s.ui.logged())
			}
		})
	}
}

// C11, the phone: a message for the agent written on the phone while the
// record cannot be written. It is not on the record, so the run that ends
// on the failure does not type it, and the next run, which reads the
// thread on from where the record says, takes it, once: where the ending
// run once typed it and the next took it again.
func TestK2APhoneMessageTheRecordCouldNotTakeIsTakenOnce(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	// Every look acts, so the record holds what the run saw: a look that
	// left no record yet (its batch is written at the end, which cannot be
	// written here) would differ from the record by nothing of this test's.
	g := phoneStart(t, fp, "hello", true, func(_ string, m []map[string]any) reply {
		if phoneLastIsTool(m) {
			return hold()
		}
		return say("Noted.")
	}, nil)
	waitFor(t, "the thread to exist", func() bool { return fp.postCount() >= 1 })
	g.rt.sync(func() {})
	before := len(g.seat.sentTexts())
	g.rt.sync(func() { _ = g.rt.sess.Close() })
	fp.userReply("run the tests")
	g.exit(10 * time.Second)
	if n := len(g.seat.sentTexts()) - before; n != 0 {
		t.Fatalf("the run that could not record the message typed it %d times", n)
	}
	g.resume(Options{Project: g.project, Interactive: true})
	taken := func() int {
		n := 0
		for _, ev := range g.log() {
			var d event.UserMessageData
			if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == "run the tests" {
				n++
			}
		}
		return n
	}
	waitFor(t, "the next run to take it", func() bool { return taken() >= 1 })
	g.running(10*loopPoll, "after the message was taken")
	if n := taken(); n != 1 {
		t.Fatalf("taken %d times, want once", n)
	}
}

// B18: two messages for the agent since the last look ended differently,
// one typed into its terminal and one that did not go. Each outcome's
// sentence names the message it means, so the model can tell which to
// send again; with one outcome among them the sentences read as before.
func TestK2EachOutcomeSaysWhichMessageItMeans(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	send := func(text, id string, ok bool) {
		m := rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text}))
		rt.append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: id, From: "user", Seq: m.Seq, Mode: "queue", Text: text, Auto: true}))
		d := event.RelayDeliveredData{Name: "codex", ID: id, OK: ok}
		if !ok {
			d.Outcome = event.DeliveryNotSent
		}
		rt.append(event.New(event.RelayDelivered, event.ActorHarness, d))
	}
	send("run the tests", "k2a", true)
	one := rt.userTail("Codex")
	send("and update the docs", "k2b", false)
	both := rt.userTail("Codex")
	if strings.Contains(one, `"run the tests"`) {
		t.Fatalf("with one outcome the sentence names its message: %q", one)
	}
	for _, s := range strings.SplitAfter(both, ". ") {
		switch {
		case strings.Contains(s, "typed into") && !strings.Contains(s, `"run the tests"`):
			t.Fatalf("the sentence for the message that arrived does not say which it is: %q", both)
		case strings.Contains(s, "did not go") && !strings.Contains(s, `"and update the docs"`):
			t.Fatalf("the sentence for the message that did not go does not say which it is: %q", both)
		}
	}
	if !strings.Contains(both, "typed into") || !strings.Contains(both, "did not go") {
		t.Fatalf("setup: %q", both)
	}
}

// A call is out on the model eagent is on when the person picks another;
// the old one then refuses it, as too long or with a failure of its own.
// That is the old model's answer: it counts no failure against the new
// one and decides nothing about restarting the conversation on it. (The
// call is made again on the new model, complete's own rule for a route
// that moved while a call was out; its answer is what counts.)
func TestK2AFailureFromTheModelMovedOffCountsNothingOnTheNewOne(t *testing.T) {
	for _, c := range []struct {
		name string
		rep  reply
	}{{"too long", r7TooLong}, {"a failure", k1BadRequest}} {
		t.Run(c.name, func(t *testing.T) {
			gate := make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(gate) }) }
			t.Cleanup(release)
			var ready atomic.Bool
			out := make(chan struct{}, 1)
			s := startSession(t, "", func(_ string, m []map[string]any) reply {
				if ready.Load() && m[len(m)-1]["role"] != "tool" {
					select {
					case out <- struct{}{}:
					default:
					}
					r := c.rep
					r.block = gate
					return r
				}
				return hold()
			}, nil)
			r5LogOnFailure(s)
			s.settled()
			ready.Store(true)
			s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "@e status?", Route: "eagent"}) })
			<-out
			var newCalls atomic.Int32
			k1Swap(t, s, 200_000, 2048, func(string, []map[string]any) reply { newCalls.Add(1); return hold() })
			release()
			r6Until(t, "the turn to end", func() bool {
				busy := true
				s.rt.sync(func() { busy = s.rt.turns.busy })
				return !busy && newCalls.Load() > 0 || r4Logged(s, "model call failed") > 0 || r4Logged(s, "refused the prompt") > 0
			})
			s.settled()
			var failures int
			var overflowed bool
			s.rt.sync(func() { failures, overflowed = s.rt.turns.failures, s.rt.turns.overflowed })
			if failures != 0 || overflowed || r6Dossiers(s) != 0 {
				t.Fatalf("the old model's answer counted on the new one: %d failures, refused %v, %d restarts; logs %q", failures, overflowed, r6Dossiers(s), s.ui.logged())
			}
			if newCalls.Load() == 0 {
				t.Fatal("the call was not made again on the new model")
			}
		})
	}
}

// Two agents are on the record: the first has exited (its terminal is
// gone), the second runs. When eagent's model is not answering, the notice
// names the one still running, as eagent's own closing words do
// (fallbackFinal), and does not say the user's messages go straight to an
// agent: with two on the record they are kept for eagent (routeUserMessage).
func TestK2TheDownNoticeNamesTheAgentsStillRunning(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	rt.append(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", AgentLabel: "Claude", Label: "Claude"}))
	rt.append(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "codex", State: "exited"}))
	text := rt.unreachableNotice(errors.New("timeout")).Text
	if strings.Contains(text, "Codex") || strings.Contains(text, "straight to") || !strings.Contains(text, "Claude") {
		t.Fatalf("with Codex exited and Claude running, told: %q", text)
	}
}

// C2, the edited window: no restart could make room on a 60k window. The
// session is stopped, the same model's window is raised in the config,
// and it is resumed. The user pastes more than the new window holds: the
// conversation as it is is refused, and a restart, which gives the paste
// by place, would fit. The decision was found on less room than is in
// force now, so it does not stand: the restart is made and the user is
// answered.
func TestK2ADecisionFoundOnLessRoomDoesNotStandAfterTheWindowIsRaised(t *testing.T) {
	setFor(t, &errorRetryDelays, []time.Duration{5 * time.Millisecond})
	route := &r5PictureRoute{window: 60000, ceiling: 2048, picture: 80000}
	inner := route.brain()
	var raised atomic.Bool
	brain := func(model string, m []map[string]any) reply {
		r := inner(model, m)
		if raised.Load() && r.status == 0 && dossierRequest(m) == "" && m[len(m)-1]["role"] != "tool" {
			return say("Here it is.")
		}
		return r
	}
	s := startSession(t, "", brain, func(c *config.Config) {
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
	cfg.Model.ContextTokens = 70000
	route.mu.Lock()
	route.window = 70000
	route.mu.Unlock()
	route.refuseAll.Store(false)
	raised.Store(true)
	s2 := audit2Resume(t, cfg, Options{Project: project, Interactive: true}, path, s.seat, s.model)
	r5LogOnFailure(s2)
	paste := "the log: " + strings.Repeat("a line of the build log ", 12000) // ~82k tokens by the estimate
	s2.rt.sync(func() { s2.rt.userMessage(event.UserMessageData{Text: paste, Route: "eagent"}) })
	r4Until(t, "an answer, or the message told it waits", func() bool {
		return r5Told(s2, "Here it is.") > 0 || r5Told(s2, stillFullNotice+`"the log:`) > 0
	})
	if r5Told(s2, "Here it is.") == 0 {
		t.Fatalf("on the raised window: %d restarts, and the user was told their message waits; a restart there makes room", r6Dossiers(s2))
	}
}
