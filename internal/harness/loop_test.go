package harness

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

const brief = "Merge every pending PR, then do a deep dependency upgrade. On MAIN, no branches!!"

// What the user writes is typed into the agent's terminal as written, with
// no model call in between, and a model that then has nothing to say leaves
// nothing in the record but its cost.
func TestUserMessageGoesToTheAgentAsWrittenAndSilentLooksLeaveNoRecord(t *testing.T) {
	s := startSession(t, brief, func(string, []map[string]any) reply { return hold() }, nil)
	s.waitFor("the brief to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	if got := s.seat.sentTexts()[0]; got != brief {
		t.Fatalf("the agent was sent %q, want the user's words unchanged", got)
	}
	s.waitFor("a look at the turn's end", func() bool {
		seen, turn := int64(0), s.events(event.RelayTurn)
		s.rt.sync(func() { seen = s.rt.turns.seen })
		return len(turn) == 1 && seen >= turn[0].Seq
	})
	s.settled()
	if evs := s.events(event.Assistant, event.Steer, event.TurnStart, event.TurnEnd, event.ToolResult, event.Message); len(evs) > 0 {
		t.Fatalf("silent looks left %d events in the record, first %s", len(evs), evs[0].Type)
	}
	var user event.UserMessageData
	userEv := s.events(event.UserMessage)[0]
	_ = userEv.Decode(&user)
	if user.Held != "" {
		t.Fatalf("the message was held (%q)", user.Held)
	}
	var send event.RelaySendData
	_ = s.events(event.RelaySend)[0].Decode(&send)
	if send.From != "user" || !send.Auto || send.Seq != userEv.Seq || send.Text != brief || send.Mode != "queue" {
		t.Fatalf("send = %+v", send)
	}
	var seen int64
	s.rt.sync(func() { seen = s.rt.turns.seen })
	if turn := s.events(event.RelayTurn); len(turn) != 1 || seen < turn[0].Seq {
		t.Fatalf("the turn's end was not looked at (seen %d)", seen)
	}
	// The looks' cost reaches the log when the run stops, in one line.
	s.stop()
	evs, err := store.Read(s.rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	var checks event.ChecksData
	for _, ev := range evs {
		if ev.Type == event.Checks {
			_ = ev.Decode(&checks)
		}
	}
	if checks.Count < 1 || checks.Usage.Input == 0 || checks.SeenSeq == 0 {
		t.Fatalf("checks = %+v", checks)
	}
	if st := state.Replay(evs); st.Totals[event.ActorEagent].Input != checks.Usage.Input {
		t.Fatalf("the silent looks' tokens are not in the totals: %+v", st.Totals)
	}
}

// When the model acts, the turn goes on the record: the lines it was given,
// its reply, the result. A reply that only acts ends the turn in one call.
func TestTurnEndIsToldToTheUserInOneRecordedCall(t *testing.T) {
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "finished a turn") {
			return say("Codex merged #42 and #44; main is green at c92b5bad.")
		}
		return hold()
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	if got := s.ui.said()[0]; got != "Codex merged #42 and #44; main is green at c92b5bad." {
		t.Fatalf("said %q", got)
	}
	var types []string
	for _, ev := range s.events(event.TurnStart, event.Steer, event.Assistant, event.ToolResult, event.Message, event.TurnEnd) {
		types = append(types, ev.Type)
	}
	want := []string{event.TurnStart, event.Steer, event.Assistant, event.Message, event.ToolResult, event.TurnEnd}
	if strings.Join(types, " ") != strings.Join(want, " ") {
		t.Fatalf("recorded %v, want %v", types, want)
	}
	var steer event.SteerData
	_ = s.events(event.Steer)[0].Decode(&steer)
	if !strings.HasPrefix(steer.Text, "[eagent ") || !strings.Contains(steer.Text, "Codex finished a turn") {
		t.Fatalf("steer = %q", steer.Text)
	}
	var a event.AssistantData
	_ = s.events(event.Assistant)[0].Decode(&a)
	if a.SeenSeq < s.events(event.RelayTurn)[0].Seq {
		t.Fatalf("the reply did not see the turn's end (seen %d)", a.SeenSeq)
	}
	calls := 0
	for _, q := range s.model.requests(testModel) {
		if bytes.Contains(q.Messages[len(q.Messages)-1], []byte("finished a turn")) {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("the turn's end took %d model calls, want 1", calls)
	}
}

// A message for eagent itself, or about the session, is kept from the
// agent and answered by eagent.
func TestMessagesForEagentAreHeldAndAnswered(t *testing.T) {
	var mu sync.Mutex
	var sawNote bool
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		mu.Lock()
		sawNote = sawNote || strings.Contains(allText(msgs), "[Not passed to the agent: it is addressed to you.]")
		mu.Unlock()
		if strings.Contains(tail, "kept for you") {
			return say("Codex is idle; nothing has been asked of it yet.")
		}
		return hold()
	}, nil)
	s.type_("@e what is Codex up to?")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.type_("how's it going?")
	s.waitFor("the second answer", func() bool { return s.ui.messageCount() == 2 })
	s.settled()
	if sent := s.seat.sentTexts(); len(sent) != 0 {
		t.Fatalf("the agent was sent %q", sent)
	}
	var held []string
	for _, ev := range s.events(event.UserMessage) {
		var d event.UserMessageData
		_ = ev.Decode(&d)
		held = append(held, d.Held)
	}
	if strings.Join(held, "|") != "it is addressed to you|it asks about the session" {
		t.Fatalf("held = %q", held)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawNote {
		t.Fatal("the model was not told the message stayed with it")
	}
}

// The prefix of every request is the bytes of the one before it. The
// system prompt and the tool list never change; the conversation only
// grows at its end; and the last message of a look that left no record is
// the only thing a later request may lack.
func TestRequestPrefixIsByteStableAcrossLooksAndTurns(t *testing.T) {
	var mu sync.Mutex
	looked := false
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		tail, all := lastUserText(msgs), allText(msgs)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(tail, "finished a turn") && !looked:
			looked = true
			return reply{calls: []event.ToolCall{tc("relay_screen", `{}`)}}
		case msgs[len(msgs)-1]["role"] == "tool" && !strings.Contains(all, "merged the PRs"):
			return say("Codex merged the PRs.")
		case strings.Contains(tail, "kept for you"):
			return say("Still here.")
		}
		return hold()
	}, nil)
	s.waitFor("the first report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	s.type_("now bump the dependencies")
	s.waitFor("the second message to reach the agent", func() bool { return len(s.seat.sentTexts()) == 2 })
	s.settled()
	s.type_("@e are you there")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 2 })
	s.settled()

	reqs := s.model.requests(testModel)
	if len(reqs) < 4 {
		t.Fatalf("only %d model calls", len(reqs))
	}
	dropped, extended := 0, 0
	for i, q := range reqs {
		if !bytes.Equal(q.Tools, reqs[0].Tools) {
			t.Fatalf("call %d: the tool list changed", i)
		}
		if !bytes.Equal(q.Messages[0], reqs[0].Messages[0]) {
			t.Fatalf("call %d: the system prompt changed", i)
		}
		// One effort, one output limit, one cache key: nothing in the
		// request but its messages differs from call to call.
		if q.Rest != reqs[0].Rest {
			t.Fatalf("call %d: the request changed outside its messages:\nfirst %s\nnow   %s", i, reqs[0].Rest, q.Rest)
		}
		if i == 0 {
			continue
		}
		prev := reqs[i-1].Messages
		common := 0
		for common < len(prev) && common < len(q.Messages) && bytes.Equal(prev[common], q.Messages[common]) {
			common++
		}
		switch common {
		case len(prev):
			extended++
		case len(prev) - 1:
			dropped++ // the previous call's last message was for that call only
		default:
			t.Fatalf("call %d shares %d of the previous call's %d messages:\nprev %s\nnow  %s", i, common, len(prev), prev[common], q.Messages[common])
		}
	}
	if dropped == 0 || extended == 0 {
		t.Fatalf("want both kinds of step; dropped tail %d, extended %d", dropped, extended)
	}
	if !strings.Contains(reqs[0].Rest, `"reasoning_effort"`) || !strings.Contains(reqs[0].Rest, `"max_completion_tokens"`) && !strings.Contains(reqs[0].Rest, `"max_tokens"`) {
		t.Fatalf("the request carries no effort or output limit to hold steady: %s", reqs[0].Rest)
	}
	if !bytes.Contains(reqs[0].Tools, []byte(`"relay_screen"`)) || bytes.Count(reqs[0].Tools, []byte(`"type":"function"`)) != 15 {
		t.Fatalf("tools = %s", reqs[0].Tools)
	}
}

// A tool that looks at something gives the model another call to use what
// it saw; the look, the result and what followed are all on the record.
func TestALookGetsAnotherCallInTheSameTurn(t *testing.T) {
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return say("Its screen shows the prompt; it is done.")
		case strings.Contains(tail, "finished a turn"):
			return reply{calls: []event.ToolCall{tc("relay_screen", `{"lines":20}`)}}
		}
		return hold()
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	if n := len(s.events(event.TurnStart)); n != 1 {
		t.Fatalf("%d turns, want 1", n)
	}
	if n := len(s.events(event.Assistant)); n != 2 {
		t.Fatalf("%d replies in the turn, want 2", n)
	}
	var res event.ToolResultData
	_ = s.events(event.ToolResult)[0].Decode(&res)
	if res.Name != "relay_screen" || !strings.Contains(res.Output, "Ask Codex to do anything") {
		t.Fatalf("result = %+v", res)
	}
}

// Words with no tool call reach no one. The model is told so once, off the
// record; if it still calls nothing the look ends in silence.
func TestTextWithoutAToolCallIsSentBackOnce(t *testing.T) {
	var mu sync.Mutex
	reminded := 0
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(tail, "had no tool call"):
			reminded++
			return say("Codex merged the PRs.")
		case strings.Contains(tail, "finished a turn"):
			return reply{text: "Codex merged the PRs."}
		}
		return hold()
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	mu.Lock()
	n := reminded
	mu.Unlock()
	if n != 1 {
		t.Fatalf("reminded %d times", n)
	}
	// The exchange that was sent back is not in the record: one reply.
	if a := s.events(event.Assistant); len(a) != 1 {
		t.Fatalf("%d replies recorded, want 1", len(a))
	}
}

// The user never hears the machinery's names. A message that uses them is
// sent back once, unsent and unrecorded; the rewrite goes out.
func TestMessageNamingTheMachineryIsRewrittenOnce(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if !strings.Contains(allText(msgs), "finished a turn") {
			return hold()
		}
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts == 1 {
			return say("The harness saw Codex finish; the dossier has the details.")
		}
		if last := msgs[len(msgs)-1]; last["role"] != "tool" || !strings.Contains(allText(msgs), "names internal machinery (harness, dossier)") {
			return say("the rewrite request did not arrive")
		}
		return say("Codex finished: the PRs are merged.")
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	if got := s.ui.said(); len(got) != 1 || got[0] != "Codex finished: the PRs are merged." {
		t.Fatalf("said %q", got)
	}
	if a := s.events(event.Assistant); len(a) != 1 {
		t.Fatalf("%d replies recorded, want only the rewrite", len(a))
	}
}

// While the agent works, eagent looks now and then. A look with nothing
// new costs no model call; one with news gets a call; the quiet limit
// makes it speak.
func TestProgressLooksFollowTheWork(t *testing.T) {
	old := narrateGap
	narrateGap = 0
	defer func() { narrateGap = old }()
	var mu sync.Mutex
	var tails []string
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		mu.Lock()
		tails = append(tails, tail)
		mu.Unlock()
		if strings.Contains(tail, "send a progress line now") {
			return say("Codex is still upgrading the dependencies: go.mod is done, tests are running.")
		}
		return hold()
	}, func(cfg *config.Config) { cfg.QuietSeconds = 3600 })
	s.seat.mu.Lock()
	s.seat.hang = true
	s.seat.mu.Unlock()
	s.type_("upgrade the dependencies")
	s.waitFor("the message to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	base := s.model.count(testModel)

	// Nothing new: a tick makes no call.
	s.rt.sync(func() { s.rt.maybeLook() })
	s.settled()
	if got := s.model.count(testModel); got != base {
		t.Fatalf("a look with nothing new made %d model calls", got-base)
	}

	// The agent did something: the next tick looks, and the look lists it.
	s.seat.emit(agentDoes("running go get -u ./..."), agentDoes("running go test ./..."))
	s.waitFor("the steps to be noted", func() bool {
		n, _ := s.rt.relays.stepped(time.Time{})
		return n == 2
	})
	s.rt.sync(func() { s.rt.maybeLook() })
	s.waitFor("a look", func() bool { return s.model.count(testModel) == base+1 })
	s.settled()
	mu.Lock()
	last := tails[len(tails)-1]
	mu.Unlock()
	for _, want := range []string{"A look while the work goes on.", "Codex is in the middle of a turn", "Right now: running go test ./...", "- ", "running go get -u ./..."} {
		if !strings.Contains(last, want) {
			t.Fatalf("the look's tail lacks %q:\n%s", want, last)
		}
	}
	if s.ui.messageCount() != 0 || len(s.events(event.Assistant)) != 0 {
		t.Fatal("a look that held left a message or a record")
	}

	// The same steps again are not news: no call.
	s.rt.sync(func() { s.rt.maybeLook() })
	s.settled()
	if got := s.model.count(testModel); got != base+1 {
		t.Fatalf("a second look at the same steps made a call")
	}

	// Past the quiet limit the model is told to speak, and does.
	s.rt.sync(func() {
		s.rt.activeSettings.QuietSeconds = 1
		s.rt.turns.lookedAt = time.Time{}
		s.rt.turns.saidAt = time.Now().Add(-time.Minute)
		s.rt.maybeLook()
	})
	s.waitFor("the progress line", func() bool { return s.ui.messageCount() == 1 })
}

func agentDoes(text string) relay.Event { return relay.Event{Kind: relay.KindActivity, Text: text} }

// A model that cannot be reached is tried again, further and further
// apart; after the second failure the user is told once, in the harness's
// own words; and what asked for the look is still owed one when the model
// comes back.
func TestModelFailureBacksOffTellsTheUserOnceAndRecovers(t *testing.T) {
	old := errorRetryDelays
	errorRetryDelays = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
	defer func() { errorRetryDelays = old }()
	var mu sync.Mutex
	failing := true
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		if failing {
			return reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}
		}
		if strings.Contains(lastUserText(msgs), "kept for you") {
			return say("Back. Codex is idle.")
		}
		return hold()
	}, nil)
	s.type_("@e status?")
	s.waitFor("the outage notice", func() bool { return s.ui.messageCount() == 1 })
	if got := s.ui.said()[0]; !strings.Contains(got, "can't reach my own model") || !strings.Contains(got, "Your messages still go straight to Codex") {
		t.Fatalf("notice = %q", got)
	}
	s.waitFor("more failures", func() bool { return len(s.events(event.Error)) >= 4 })
	if n := s.ui.messageCount(); n != 1 {
		t.Fatalf("the user was told %d times", n)
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	s.waitFor("the answer once the model is back", func() bool { return s.ui.messageCount() == 2 })
	if got := s.ui.said()[1]; got != "Back. Codex is idle." {
		t.Fatalf("said %q", got)
	}
	// The notice is in the model's conversation as something eagent said
	// itself, so it neither repeats nor contradicts it.
	reqs := s.model.requests(testModel)
	shown := false
	for _, m := range reqs[len(reqs)-1].Messages {
		shown = shown || bytes.Contains(m, []byte("eagent told the user this itself"))
	}
	if !shown {
		t.Fatal("the model was not shown the notice")
	}
}

// While the model is failing, a message for the agent still goes straight
// to it.
func TestMessagesReachTheAgentWhileTheModelIsDown(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply {
		return reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}
	}, nil)
	s.type_("run the tests")
	s.waitFor("the message to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	if got := s.seat.sentTexts()[0]; got != "run the tests" {
		t.Fatalf("sent %q", got)
	}
}

// A session whose agent is running does not end by itself, and stopping
// the agent on purpose ends a session that has no terminal.
func TestBatchSessionLivesAsLongAsItsAgent(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("Done: the PRs are merged.")
	useFakeRelays(t, seat)
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "finished a turn"):
			return reply{calls: []event.ToolCall{
				tc("send_message", `{"text":"Codex merged the PRs; stopping it as you asked.","important":true}`),
				tc("relay_control", `{"action":"stop","reason":"the work is done"}`),
			}}
		}
		return hold()
	})
	defer model.srv.Close()
	ui := &fakeUI{}
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: brief + " Stop when done."}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}()
	select {
	case code := <-done:
		done <- code
		if code != 0 {
			t.Fatalf("exit code %d; logs %v", code, ui.logs)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end after its agent was stopped")
	}
	if !seat.wasKilled() {
		t.Fatal("the agent was not stopped")
	}
	evs, _ := store.Read(rt.sess.Path)
	st := state.Replay(evs)
	if !st.Ended || st.EndReason != "done" || len(st.ActiveRelays()) != 0 {
		t.Fatalf("ended=%v reason=%q", st.Ended, st.EndReason)
	}
	if said := ui.said(); len(said) != 1 || !strings.Contains(said[0], "merged the PRs") {
		t.Fatalf("said %q", said)
	}
}
