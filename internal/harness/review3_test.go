package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// Tests for the second adversarial review of the one-loop design (the
// findings that touch the loop, the dossier's coverage and the rendering).

// With one call to a turn there is no second call to answer a reply that
// was sent back. Such a reply stands as it came and the wake is settled,
// so the same message does not start one paid call after another. What is
// still owed is asked for again later, on the delays every owed answer
// has.
func TestOneCallTurnsDoNotRetryTheSameWakeAtOnce(t *testing.T) {
	old := owedRetryDelays
	owedRetryDelays = []time.Duration{time.Hour, time.Hour, time.Hour}
	defer func() { owedRetryDelays = old }()
	cases := []struct {
		name  string
		reply func() reply
		said  int
	}{
		{"hold", hold, 0},
		{"text only", func() reply { return reply{text: "Codex is idle."} }, 0},
		{"cut off", func() reply { return reply{text: "Codex is", finish: "length"} }, 0},
		// Words that would be sent back for a rewrite go out as they are.
		{"names the machinery", func() reply { return say("The orchestrator is idle.") }, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := startSession(t, "", func(string, []map[string]any) reply { return c.reply() }, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 1 })
			s.settled()
			before := s.model.count(testModel)
			s.type_("@e what is Codex up to?")
			s.waitFor("the call for the message", func() bool { return s.model.count(testModel) > before })
			// A retry at once starts on the tick that ends the turn; four
			// more ticks for good measure.
			s.waitFor("the turn to end", func() bool {
				busy := true
				s.rt.sync(func() { busy = s.rt.turns.busy })
				return !busy
			})
			time.Sleep(4 * loopPoll)
			if n := s.model.count(testModel) - before; n != 1 {
				t.Fatalf("one message made %d calls at once with one call to a turn", n)
			}
			s.settled()
			if got := s.ui.messageCount(); got != c.said {
				t.Fatalf("the user was told %d things, want %d", got, c.said)
			}
			var owed int64
			var armed bool
			s.rt.sync(func() { owed, armed = s.rt.turns.owed, s.rt.turns.owedTimer != nil })
			if c.said == 0 && (owed == 0 || !armed) {
				t.Fatalf("the unanswered message is not asked for again later (owed %d, timer %v)", owed, armed)
			}
			if c.said == 1 && owed != 0 {
				t.Fatal("an answered message is still owed an answer")
			}
		})
	}
}

// A model that never answers is asked a bounded number of times: once for
// the message and once per delay, and then eagent says in its log that the
// message went unanswered.
func TestOneCallTurnsGiveUpOnAModelThatAlwaysHolds(t *testing.T) {
	old := owedRetryDelays
	owedRetryDelays = []time.Duration{30 * time.Millisecond, 30 * time.Millisecond, 30 * time.Millisecond}
	defer func() { owedRetryDelays = old }()
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 1 })
	s.settled()
	before := s.model.count(testModel)
	s.type_("@e what is Codex up to?")
	s.waitFor("eagent to give up", func() bool {
		for _, l := range s.ui.logged() {
			if strings.Contains(l, "went unanswered") {
				return true
			}
		}
		return false
	})
	time.Sleep(200 * time.Millisecond)
	if n := s.model.count(testModel) - before; n != 1+len(owedRetryDelays) {
		t.Fatalf("a model that always holds was called %d times, want %d", n, 1+len(owedRetryDelays))
	}
}

// A resumed session knows how many calls the conversation in its current
// file has made and how long its first prompt was. So when the provider
// refuses the first request after the resume as too long, the conversation
// is restarted from a dossier, as it would have been without the stop: the
// first call of the process is not taken for the first call of the
// conversation, which would mean a restart cannot help.
func TestResumedConversationThatIsRefusedIsRestartedFromADossier(t *testing.T) {
	var prompt atomic.Int64
	var tooLong atomic.Bool
	brain := func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			tooLong.Store(false)
			p := places(ask)
			return reply{text: briefing(p[:min(len(p), 4)])}
		}
		if tooLong.Load() {
			return reply{status: 400, body: tooLongBody}
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			r = say("Here.")
		}
		if p := prompt.Swap(0); p > 0 {
			r.prompt = int(p)
		}
		return r
	}
	s := startSession(t, brief, brain, nil)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	prompt.Store(21_000)
	s.type_("@e hello")
	s.waitFor("the first dossier", func() bool { return len(s.dossiers()) == 1 })
	s.waitFor("the first answer", func() bool { return s.ui.messageCount() >= 1 })
	s.settled()
	// A call that works in the restarted conversation.
	said := s.ui.messageCount()
	prompt.Store(3_000)
	s.type_("@e and again")
	s.waitFor("the answer in the new conversation", func() bool { return s.ui.messageCount() > said })
	s.settled()
	var calls, first int
	s.rt.sync(func() { calls, first = s.rt.turns.subCalls, s.rt.turns.firstPrompt })
	if calls == 0 || first != 3_000 {
		t.Fatalf("before the stop: %d calls, first prompt %d", calls, first)
	}
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()

	// The provider refuses the very first request of the new process.
	tooLong.Store(true)
	useFakeRelays(t, newFakeSeat("unused"))
	ui := &fakeUI{input: make(chan string, 8)}
	rt, err := Resume(testConfig(s.model.srv.URL), Options{Project: project, Interactive: true}, ui, path)
	if err != nil {
		t.Fatal(err)
	}
	gotCalls, gotFirst := rt.turns.subCalls, rt.turns.firstPrompt // before the loop starts
	s2 := &session{t: t, rt: rt, ui: ui, model: s.model, done: make(chan int, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s2.cancel = cancel
	go func() { s2.done <- rt.Run(ctx) }()
	t.Cleanup(s2.stop)
	s2.type_("@e are you there?")
	gaveUp := func() string {
		for _, m := range s2.ui.said() {
			if strings.HasPrefix(m, fullNotice) {
				return m
			}
		}
		return ""
	}
	s2.waitFor("the dossier after the resume", func() bool { return len(s2.dossiers()) == 2 || gaveUp() != "" })
	if m := gaveUp(); m != "" {
		t.Fatalf("the user was told a restart cannot help, in a conversation that had worked:\n%s", m)
	}
	s2.waitFor("the answer after the resume", func() bool { return s2.ui.messageCount() >= 1 })
	if gotCalls != calls || gotFirst != first {
		t.Fatalf("resumed with %d calls and a first prompt of %d, want %d and %d", gotCalls, gotFirst, calls, first)
	}
}

// A batch of looks that left no record says how long the first of them
// was, so a conversation whose first calls were all silent is rebuilt with
// its real first prompt and not a later, longer one.
func TestSilentLooksRecordTheirFirstPrompt(t *testing.T) {
	var n atomic.Int64
	s := startSession(t, brief, func(string, []map[string]any) reply {
		r := hold()
		r.prompt = int(1000 * n.Add(1))
		return r
	}, nil)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	s.type_("and then tag the release")
	s.waitFor("the second turn", func() bool { return len(s.events(event.RelayTurn)) == 2 })
	s.settled()
	var first, calls int
	s.rt.sync(func() { first, calls = s.rt.turns.firstPrompt, s.rt.turns.subCalls })
	if calls < 2 || first != 1000 {
		t.Fatalf("live: %d calls, first prompt %d", calls, first)
	}
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()
	useFakeRelays(t, newFakeSeat("unused"))
	rt, err := Resume(testConfig(s.model.srv.URL), Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 8)}, path)
	if err != nil {
		t.Fatal(err)
	}
	if rt.turns.firstPrompt != first || rt.turns.subCalls != calls {
		t.Fatalf("resumed with a first prompt of %d and %d calls, want %d and %d", rt.turns.firstPrompt, rt.turns.subCalls, first, calls)
	}
}

// The provider refuses the session's very first request as too long, so
// the conversation is restarted from a dossier written before any call had
// seen anything. The message that set it off, with its picture, is in the
// conversation that follows: the dossier took in nothing, and says so.
func TestRefusedFirstRequestKeepsTheMessageThatSetItOff(t *testing.T) {
	var tooLong atomic.Bool
	tooLong.Store(true)
	var mu sync.Mutex
	var after []map[string]any // the first ordinary request after the restart
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			tooLong.Store(false)
			p := places(ask)
			return reply{text: briefing(p[:min(len(p), 4)])}
		}
		if tooLong.Load() {
			return reply{status: 400, body: tooLongBody}
		}
		mu.Lock()
		if after == nil {
			after = msgs
		}
		mu.Unlock()
		if strings.Contains(allText(msgs[len(msgs)-1:]), "kept for you") {
			return say("Here.")
		}
		return hold()
	}, nil)
	shot := filepath.Join(s.rt.opts.Project, "pond.png")
	if err := os.WriteFile(shot, tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	s.rt.sync(func() {
		s.rt.userMessage(event.UserMessageData{
			Text:        "@e the heron lands at dusk",
			Attachments: []event.Attachment{{Name: "pond.png", Path: shot, ContentType: "image/png", Kind: "image"}},
		})
	})
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	s.waitFor("the answer", func() bool { return s.ui.messageCount() >= 1 })
	if d := s.dossiers()[0]; d.CoversSeq != 0 || !d.CoversNone {
		t.Fatalf("the dossier says it took in up to %d (none: %v), and no call had seen anything", d.CoversSeq, d.CoversNone)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(allText(after), "the heron lands at dusk") {
		t.Fatalf("the message that set the restart off is not in the new conversation:\n%s", allText(after))
	}
	pictures := 0
	for _, m := range after {
		if parts, ok := m["content"].([]any); ok && m["role"] == "user" {
			for _, p := range parts {
				if pm, ok := p.(map[string]any); ok && pm["type"] == "image_url" {
					pictures++
				}
			}
		}
	}
	if pictures != 1 {
		t.Fatalf("the new conversation carries %d pictures, want the one the user sent", pictures)
	}
}

// toolContents lists the tool results of a request as sent, in order.
func toolContents(msgs []map[string]any) []string {
	var out []string
	for _, m := range msgs {
		if m["role"] == "tool" {
			s, _ := m["content"].(string)
			out = append(out, s)
		}
	}
	return out
}

// A provider that numbers its tool calls from 0 in every response gives
// two reads of the screen the same call id. Each result stays under its
// own call in every later request, so the earlier screen is not replaced
// by the later one and the prompt already sent does not change.
func TestReusedCallIDDoesNotRewriteAnEarlierResult(t *testing.T) {
	const id = "functions.relay_screen:0"
	screen := func() reply {
		return reply{calls: []event.ToolCall{{ID: id, Name: "relay_screen", Args: []byte(`{}`)}}}
	}
	var mu sync.Mutex
	reads := 0
	var seat *fakeSeat
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		tail := lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool" && reads == 1:
			// The first screen has been read; the terminal moves on and
			// is read again, under the same id.
			seat.mu.Lock()
			seat.screen = "SECOND SCREEN: tests pass"
			seat.mu.Unlock()
			reads++
			return screen()
		case msgs[len(msgs)-1]["role"] == "tool":
			return say("Codex ran the tests.")
		case strings.Contains(tail, "finished a turn") && reads == 0:
			reads++
			return screen()
		case strings.Contains(tail, "kept for you"):
			return say("Still here.")
		}
		return hold()
	}, nil)
	seat = s.seat
	seat.mu.Lock()
	seat.screen = "FIRST SCREEN: tests running"
	seat.mu.Unlock()
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	s.type_("@e are you there")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 2 })
	s.settled()

	var withBoth [][]map[string]any
	for _, q := range s.model.requests(testModel) {
		msgs := make([]map[string]any, len(q.Messages))
		for i, m := range q.Messages {
			_ = json.Unmarshal(m, &msgs[i])
		}
		if len(toolContents(msgs)) >= 2 {
			withBoth = append(withBoth, msgs)
		}
	}
	if len(withBoth) < 2 {
		t.Fatalf("only %d requests carried both reads", len(withBoth))
	}
	for i, msgs := range withBoth {
		got := toolContents(msgs)
		if !strings.Contains(got[0], "FIRST SCREEN") || !strings.Contains(got[1], "SECOND SCREEN") {
			t.Fatalf("request %d with both reads has them as:\n1: %s\n2: %s", i, got[0], got[1])
		}
	}
}
