package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// Tests that pin down the fixes made after the second behaviour audit, one
// test per fix. Each is named TestAudit2... so they run as a group.

// ---- helpers ---------------------------------------------------------------

// audit2Model is a scripted model server closed when the test ends. Created
// before the session, so its cleanup runs after the session has stopped.
func audit2Model(t *testing.T, brain func(model string, msgs []map[string]any) reply) *scripted {
	t.Helper()
	m := newScripted(brain)
	t.Cleanup(m.srv.Close)
	return m
}

// audit2Start starts a session with the given configuration and options.
// seat nil leaves the launcher as TestMain set it: no agent can be started.
// ready runs after the runtime is built and before it runs.
func audit2Start(t *testing.T, cfg config.Config, opts Options, seat *fakeSeat, model *scripted, ready func(*Runtime)) *session {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	if seat != nil {
		useFakeRelays(t, seat)
	}
	if opts.Project == "" {
		opts.Project = t.TempDir()
	}
	ui := &fakeUI{input: make(chan string, 16)}
	rt, err := New(cfg, opts, ui)
	if err != nil {
		t.Fatal(err)
	}
	if ready != nil {
		ready(rt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s := &session{t: t, rt: rt, ui: ui, seat: seat, model: model, cancel: cancel, done: make(chan int, 1)}
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	return s
}

// audit2Resume resumes a stopped session's log with cfg and runs it.
func audit2Resume(t *testing.T, cfg config.Config, opts Options, path string, seat *fakeSeat, model *scripted) *session {
	t.Helper()
	useFakeRelays(t, seat)
	ui := &fakeUI{input: make(chan string, 16)}
	rt, err := Resume(cfg, opts, ui, path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s := &session{t: t, rt: rt, ui: ui, seat: seat, model: model, cancel: cancel, done: make(chan int, 1)}
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	return s
}

// audit2Called names the tools the last assistant message of a prompt
// called.
func audit2Called(msgs []map[string]any) []string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i]["role"] != "assistant" {
			continue
		}
		var out []string
		calls, _ := msgs[i]["tool_calls"].([]any)
		for _, c := range calls {
			cm, _ := c.(map[string]any)
			f, _ := cm["function"].(map[string]any)
			n, _ := f["name"].(string)
			out = append(out, n)
		}
		return out
	}
	return nil
}

// audit2Requests decodes the prompts the model was sent, in order.
func audit2Requests(m *scripted) [][]map[string]any {
	var out [][]map[string]any
	for _, q := range m.requests(testModel) {
		msgs := make([]map[string]any, len(q.Messages))
		for i, raw := range q.Messages {
			_ = json.Unmarshal(raw, &msgs[i])
		}
		out = append(out, msgs)
	}
	return out
}

// audit2CountLast counts the prompts whose last message contains sub.
func audit2CountLast(m *scripted, sub string) int {
	n := 0
	for _, msgs := range audit2Requests(m) {
		if strings.Contains(lastText(msgs), sub) {
			n++
		}
	}
	return n
}

// audit2Results returns the recorded results of one tool, decoded.
func audit2Results(evs []event.Event, name string) []event.ToolResultData {
	var out []event.ToolResultData
	for _, ev := range evs {
		if ev.Type != event.ToolResult {
			continue
		}
		var d event.ToolResultData
		_ = ev.Decode(&d)
		if d.Name == name {
			out = append(out, d)
		}
	}
	return out
}

// audit2Messages returns what eagent told the user, decoded, from events.
func audit2Messages(evs []event.Event) []event.MessageData {
	var out []event.MessageData
	for _, ev := range evs {
		if ev.Type == event.Message {
			var d event.MessageData
			_ = ev.Decode(&d)
			out = append(out, d)
		}
	}
	return out
}

func audit2Logged(ui *fakeUI, sub string) bool {
	for _, l := range ui.logged() {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func audit2PNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ---- 1. an answer the model sits on ----------------------------------------

// When the user answers eagent's question and the model only holds, the
// reply is sent back once saying the answer has reached nobody else; a
// second hold is then taken as it is.
func TestAudit2HeldAnswerIsSentBackOnce(t *testing.T) {
	var mu sync.Mutex
	reminded := 0
	var answeredTail string
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(lastText(msgs), "The user's answer has reached nobody but you"):
			reminded++
			return hold()
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "The user answered your question"):
			answeredTail = tail
			return hold()
		case strings.Contains(tail, "kept for you"):
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge #45 as well?","options":["yes","no"]}`)}}
		}
		return hold()
	}, nil)
	s.type_("@e ask me whether to merge #45")
	s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
	s.settled()
	s.type_("yes")
	s.waitFor("the answer", func() bool { return len(s.events(event.UserAnswer)) == 1 })
	s.settled()
	mu.Lock()
	defer mu.Unlock()
	if answeredTail == "" {
		t.Fatal("the model was not told the user answered")
	}
	if reminded != 1 {
		t.Fatalf("the held answer was sent back %d times, want 1", reminded)
	}
}

// ---- 2. an unanswered message stays owed -----------------------------------

// A message kept for eagent that it left unanswered stays owed when newer
// messages arrive: a second one kept for eagent leaves the oldest as the
// mark, and an ordinary message does not clear it. The next tail still
// names every unanswered message.
func TestAudit2UnansweredMessageStaysOwedAcrossNewerMessages(t *testing.T) {
	var seen tails
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		seen.add(lastUserText(msgs))
		return hold()
	}, nil)
	owed := func() int64 {
		var o int64
		s.rt.sync(func() { o = s.rt.turns.owed })
		return o
	}
	s.type_("@e what is Codex up to?")
	s.waitFor("the message to be owed", func() bool { return owed() != 0 })
	s.settled()
	first := owed()
	if ums := s.events(event.UserMessage); len(ums) != 1 || ums[0].Seq != first {
		t.Fatalf("owed %d, user messages %+v", first, ums)
	}
	s.type_("@e and which branch is it on?")
	s.waitFor("the second message", func() bool { return len(s.events(event.UserMessage)) == 2 })
	s.settled()
	if got := owed(); got != first {
		t.Fatalf("after a second unanswered message owed is %d, want the oldest %d", got, first)
	}
	s.type_("run the unit tests")
	s.waitFor("the message to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	if got := owed(); got != first {
		t.Fatalf("an ordinary message changed owed to %d, want %d", got, first)
	}
	tail := seen.find("typed into Codex's terminal as written")
	for _, want := range []string{"Also: the user wrote to you more than once", "what is Codex up to?", "and which branch is it on?"} {
		if !strings.Contains(tail, want) {
			t.Fatalf("the tail after an ordinary message lacks %q:\n%s", want, tail)
		}
	}
}

// ---- 3. what arrives mid-turn ----------------------------------------------

// Something that arrives while a recorded turn is running gets a tail of
// its own on the turn's next call, and the look before it is not counted as
// having seen it.
func TestAudit2ArrivalMidTurnGetsATailOfItsOwn(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var mu sync.Mutex
	calls := 0
	var second []map[string]any
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		calls++
		switch calls {
		case 1:
			return reply{calls: []event.ToolCall{tc("relay_screen", `{}`)}, block: release}
		case 2:
			second = msgs
			return say("Codex's screen is clear, and I will tag v2 when the work is done.")
		}
		return hold()
	}, nil)
	s.type_("@e what is on Codex's screen?")
	s.waitFor("the first call", func() bool { return s.model.count(testModel) == 1 })
	s.type_("@e also tag v2 when it is done")
	s.waitFor("the second message", func() bool { return len(s.events(event.UserMessage)) == 2 })
	close(release)
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	late := s.events(event.UserMessage)[1].Seq
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("%d calls, want 2 (the second message answered in the same turn)", calls)
	}
	if !strings.Contains(lastUserText(second), "The user wrote, and the message was kept for you") || !strings.Contains(allText(second), "also tag v2 when it is done") {
		t.Fatalf("the second call has no tail for what arrived:\n%s", lastUserText(second))
	}
	asst := s.events(event.Assistant)
	if len(asst) != 2 {
		t.Fatalf("%d replies recorded", len(asst))
	}
	var a1, a2 event.AssistantData
	_ = asst[0].Decode(&a1)
	_ = asst[1].Decode(&a2)
	if a1.SeenSeq >= late || a2.SeenSeq < late {
		t.Fatalf("seen marks %d and %d around the late message at %d", a1.SeenSeq, a2.SeenSeq, late)
	}
	if n := len(s.events(event.Steer)); n != 2 {
		t.Fatalf("%d steers recorded, want one per tail", n)
	}
}

// ---- 4. the closing word -----------------------------------------------------

// audit2Closing runs a batch session in which eagent speaks at the agent's
// turn end, then the user writes to eagent (through the inbox), eagent
// stops the agent without answering, and the closing look is left to final.
func audit2Closing(t *testing.T, final func(msgs []map[string]any) reply) (*session, *tails, int) {
	t.Helper()
	var seen tails
	var path atomic.Pointer[string]
	var posted atomic.Bool
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		seen.add(tail)
		switch {
		case strings.Contains(tail, "The session is ending now"):
			return final(msgs)
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "kept for you"):
			return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"the work is done"}`)}}
		case strings.Contains(tail, "finished a turn") && posted.CompareAndSwap(false, true):
			if err := PostInbox(*path.Load(), InboxMessage{Type: "message", Text: "@e is #45 merged too?", From: "web"}); err != nil {
				t.Error(err)
			}
			return say("Codex merged #42 and #44.")
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief + " Stop when done."}, newFakeSeat("Merged #42 and #44."), model, func(rt *Runtime) {
		p := rt.sess.Path
		path.Store(&p)
	})
	code := coreExit(s)
	return s, &seen, code
}

// The closing word of a session with no terminal: it is owed when the user
// wrote after eagent last spoke, a hold there is sent back, plain text is
// the report, and when the model fails the fallback says how to resume.
func TestAudit2ClosingWord(t *testing.T) {
	t.Run("owed after the user wrote, and a hold is sent back", func(t *testing.T) {
		s, seen, code := audit2Closing(t, func(msgs []map[string]any) reply {
			if strings.Contains(lastText(msgs), "Holding is not allowed here") {
				return say("Final: #42 and #44 are merged; #45 is not.")
			}
			return hold()
		})
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if seen.find("The user has written since you last spoke and has heard nothing back") == "" {
			t.Fatal("the closing look did not say the user is owed a word")
		}
		if n := audit2CountLast(s.model, "Holding is not allowed here"); n != 1 {
			t.Fatalf("the hold was sent back %d times", n)
		}
		said := s.ui.said()
		if len(said) == 0 || said[len(said)-1] != "Final: #42 and #44 are merged; #45 is not." {
			t.Fatalf("said %q", said)
		}
	})
	t.Run("plain text is the report", func(t *testing.T) {
		const report = "Final: #42 and #44 are merged; #45 was never opened."
		s, _, code := audit2Closing(t, func(msgs []map[string]any) reply { return reply{text: report} })
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		msgs := audit2Messages(mustReadCore(t, s.rt.sess.Path))
		last := msgs[len(msgs)-1]
		if last.Text != report || !last.Important {
			t.Fatalf("last message %+v", last)
		}
		if n := audit2CountLast(s.model, "That reply had no tool call"); n != 0 {
			t.Fatalf("the plain-text report was sent back %d times", n)
		}
	})
	t.Run("the fallback says how to resume", func(t *testing.T) {
		s, _, _ := audit2Closing(t, func(msgs []map[string]any) reply {
			return reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}
		})
		said := s.ui.said()
		last := said[len(said)-1]
		if !strings.Contains(last, "I could not write a proper closing report") || !strings.HasSuffix(last, "`eagent -c` picks the session back up.") {
			t.Fatalf("the fallback is %q", last)
		}
	})
}

// ---- 5. the first failure is retried at once --------------------------------

// The first failed model call is tried again at once, not after a minute.
func TestAudit2FirstFailedCallIsRetriedAtOnce(t *testing.T) {
	if errorRetryDelays[0] != 0 {
		t.Fatalf("the first retry waits %s", errorRetryDelays[0])
	}
	var mu sync.Mutex
	var at []time.Time
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		at = append(at, time.Now())
		if len(at) == 1 {
			return reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}
		}
		if strings.Contains(lastUserText(msgs), "kept for you") {
			return say("Hello.")
		}
		return hold()
	}, nil)
	s.type_("@e hello")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	if !audit2Logged(s.ui, "trying again now") {
		t.Fatalf("logs %q", s.ui.logged())
	}
	mu.Lock()
	defer mu.Unlock()
	if gap := at[1].Sub(at[0]); gap > 2*time.Second {
		t.Fatalf("the retry came %s after the failure", gap)
	}
}

// ---- 6. a relay action gets another call ------------------------------------

// A successful relay_control, relay_start or relay_autopilot gives the
// model another call in the same turn, so it can tell the user what came
// of it.
func TestAudit2RelayActionGetsAnotherCall(t *testing.T) {
	for _, c := range []struct{ name, args string }{
		{"relay_control", `{"action":"stop","reason":"the user asked"}`},
		{"relay_start", `{"profile":"claude"}`},
		{"relay_autopilot", `{"instruction":"keep merging the open PRs","until":"1h"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			followed := false
			s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
				mu.Lock()
				defer mu.Unlock()
				// The call after the action: the record's last reply is the
				// action (a line it caused, such as the agent stopping, may
				// follow its result).
				called := audit2Called(msgs)
				if len(called) == 1 && called[0] == c.name {
					followed = true
					return say("Done: " + c.name + ".")
				}
				if called == nil && strings.Contains(lastUserText(msgs), "kept for you") {
					return reply{calls: []event.ToolCall{tc(c.name, c.args)}}
				}
				return hold()
			}, nil)
			useRelayFactory(t, "Ready.")
			s.type_("@e please do it")
			s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
			s.settled()
			res := audit2Results(s.events(event.ToolResult), c.name)
			if len(res) != 1 || res[0].IsError {
				t.Fatalf("result %+v", res)
			}
			mu.Lock()
			defer mu.Unlock()
			if !followed {
				t.Fatal("no call followed the result")
			}
		})
	}
}

// ---- 7. update_thread --------------------------------------------------------

// update_thread: with pin the user's words are kept as written; with
// neither field the answer is "kept as they are" and nothing is recorded;
// with summaries off an unpinned title is not set; setting the same values
// again says nothing changed.
func TestAudit2UpdateThread(t *testing.T) {
	t.Run("summaries off", func(t *testing.T) {
		s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
			if msgs[len(msgs)-1]["role"] == "tool" {
				return hold()
			}
			// The model's way of saying the title and summary still fit.
			return reply{calls: []event.ToolCall{tc("update_thread", `{}`)}}
		}, nil)
		s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
		s.settled()
		if n := len(s.events(event.Assistant)); n != 0 {
			t.Fatalf("update_thread with neither field left %d replies in the conversation", n)
		}
		if out, isErr, _ := s.rt.updateThread("", "", false); out != "kept as they are" || isErr {
			t.Fatalf("neither field: %q %v", out, isErr)
		}
		if out, isErr, _ := s.rt.updateThread("Fence repainting", "Painting.", false); !strings.HasPrefix(out, "not set: titles and summaries are switched off") || !isErr {
			t.Fatalf("unpinned with summaries off: %q %v", out, isErr)
		}
		if n := len(s.events(event.ThreadTitle)); n != 0 {
			t.Fatalf("%d thread.title events before any pin", n)
		}
		title := strings.TrimSpace(strings.Repeat("Repaint the back fence and the gate ", 3)) // over 60 runes
		summary := strings.Repeat("The user dictated this summary word for word. ", 45)[:1900]
		if out, _, _ := s.rt.updateThread(title, summary, true); out != "updated." {
			t.Fatalf("pinned: %q", out)
		}
		var got, desc string
		var pinned bool
		s.read(func(st *state.State) { got, desc, pinned = st.Title, st.Description, st.TitlePinned })
		if got != title || desc != strings.TrimSpace(summary) || !pinned {
			t.Fatalf("pinned title %q (%d runes) pinned=%v, summary %d chars", got, len([]rune(got)), pinned, len(desc))
		}
		long := strings.Repeat("y", 2500)
		s.rt.updateThread(title, long, true)
		s.read(func(st *state.State) { desc = st.Description })
		if !strings.HasPrefix(desc, strings.Repeat("y", 2000)) || len([]rune(desc)) > 2001 {
			t.Fatalf("a 2500-character summary became %d runes", len([]rune(desc)))
		}
	})
	t.Run("summaries on", func(t *testing.T) {
		coreSummariesOn(t, false)
		s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
		s.settled()
		if out, _, _ := s.rt.updateThread("Fence repainting", "Painting the back fence.", false); !strings.HasPrefix(out, "updated.") {
			t.Fatalf("first: %q", out)
		}
		if out, _, _ := s.rt.updateThread("Fence repainting", "Painting the back fence.", false); out != "nothing changed: they already say that." {
			t.Fatalf("the same again: %q", out)
		}
	})
}

// ---- 8. the title and summary are asked about again ------------------------

// audit2ThreadAsks starts a session with inline summaries on a quick
// schedule, whose model never answers about the thread, and returns when
// the silence has been taken as "they still fit". slowFirst delays the
// reply to the first call that asks well past the quick schedule's first
// timers, so they come due while it runs.
func audit2ThreadAsks(t *testing.T, slowFirst bool) (*session, func() []time.Time) {
	t.Helper()
	coreSummariesOn(t, true)
	var firstCall time.Duration
	if slowFirst {
		// Read after the quick schedule is in: ten of its first delays, and
		// five of the delay a user's message arms.
		firstCall = 10 * summaryFirstDelay
	}
	var mu sync.Mutex
	var asks, ends []time.Time
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if !strings.Contains(lastUserText(msgs), coreThreadAsk) {
			return hold()
		}
		mu.Lock()
		asks = append(asks, time.Now())
		first := len(asks) == 1
		mu.Unlock()
		if first && firstCall > 0 {
			time.Sleep(firstCall)
		}
		mu.Lock()
		ends = append(ends, time.Now())
		mu.Unlock()
		return hold()
	}, nil)
	s.seat.mu.Lock()
	s.seat.hang = true // the agent takes the message and keeps working: no news after it
	s.seat.mu.Unlock()
	s.type_("run the unit tests")
	kept := func() bool {
		for _, d := range coreTitles(s.events(event.ThreadTitle)) {
			if d.By == "summary" && d.Title == "" && d.Description == "" && d.SeenSeq > 0 {
				return true
			}
		}
		return false
	}
	s.waitFor("silence to be taken as the title and summary still fitting", kept)
	return s, func() []time.Time {
		mu.Lock()
		defer mu.Unlock()
		// Each ask, then the end of the first.
		return append(append([]time.Time{}, asks...), ends[0])
	}
}

// A model asked about the thread that says nothing about it is asked again,
// and after threadAskAgain more tries its silence is taken as "they still
// fit" and recorded so; then it is not asked again until there is news.
func TestAudit2ThreadAskedAgainThenTakenAsKept(t *testing.T) {
	_, times := audit2ThreadAsks(t, false)
	got := times()
	if n := len(got) - 1; n != 1+threadAskAgain {
		t.Fatalf("asked %d times before it was taken as kept, want %d", n, 1+threadAskAgain)
	}
	time.Sleep(4 * summaryRetry)
	if n := len(times()) - 1; n != 1+threadAskAgain {
		t.Fatalf("asked %d times in all, more after it was taken as kept", n)
	}
}

// The asks after an unanswered one keep their distance: none comes sooner
// than summaryRetry after the call that said nothing, even when the
// summary's own timer came due while that call ran.
func TestAudit2ThreadReaskWaitsItsGap(t *testing.T) {
	// The first ask takes longer than summaryFirstDelay, so the timer the
	// user's message armed comes due while it runs.
	_, times := audit2ThreadAsks(t, true)
	got := times()
	end1 := got[len(got)-1]
	if gap := got[1].Sub(end1); gap < summaryRetry*9/10 {
		t.Fatalf("asked again %s after a call that said nothing, want at least %s", gap, summaryRetry)
	}
}

// ---- 9. session_read is bounded ----------------------------------------------

// session_read in the main loop is cut at loopReadChars characters.
func TestAudit2SessionReadIsBounded(t *testing.T) {
	if loopReadChars != 24000 {
		t.Fatalf("loopReadChars = %d", loopReadChars)
	}
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	var file string
	s.rt.sync(func() {
		file = s.rt.sess.Current()
		for i := 0; i < 120; i++ {
			s.rt.append(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "codex", Text: fmt.Sprintf("step %d ", i) + strings.Repeat("z", 600)}))
		}
	})
	s.settled()
	out, isErr, _ := s.rt.runTool(context.Background(), &turnCalls{}, tc("session_read", fmt.Sprintf(`{"file":%q,"from":1,"to":500}`, file)))
	if isErr {
		t.Fatal(out)
	}
	if len(out) > loopReadChars+1000 || !strings.Contains(out, "output limit reached") {
		t.Fatalf("a read of 120 long lines returned %d characters", len(out))
	}
}

// A raw session_read of one line longer than loopReadChars is cut too.
func TestAudit2SessionReadRawLineIsBounded(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	var file string
	var line int
	s.rt.sync(func() {
		file = s.rt.sess.Current()
		// A user can paste this much; the record keeps it whole.
		ev := s.rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: strings.Repeat("w", 30000)}))
		line = ev.Source.Line
	})
	out, _, _ := s.rt.runTool(context.Background(), &turnCalls{}, tc("session_read", fmt.Sprintf(`{"file":%q,"from":%d,"to":%d,"raw":true}`, file, line, line)))
	if len(out) > loopReadChars+1000 {
		t.Fatalf("a raw read of one 30000-character line returned %d characters", len(out))
	}
}

// ---- 10. the agent's usage in the tail --------------------------------------

// The tail's line for an agent says where its usage budget stands when the
// agent has reported it.
func TestAudit2AgentLineCarriesItsUsage(t *testing.T) {
	var seen tails
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		seen.add(lastUserText(msgs))
		return hold()
	}, nil)
	s.seat.emit(relay.Event{Kind: relay.KindLimit, UsedPercent: 73, Window: "5h"})
	s.waitFor("the limit", func() bool { return len(s.events(event.RelayLimit)) == 1 })
	s.settled()
	s.type_("run the unit tests")
	s.waitFor("the message to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	tail := seen.find("typed into Codex's terminal")
	if !strings.Contains(tail, "Usage: 73% of its 5h budget used.") {
		t.Fatalf("the agent's line has no usage:\n%s", tail)
	}
}

// ---- 11. a turn cut off by a restart ----------------------------------------

// A resume after a turn that was cut off tells eagent so, and a resume
// after a turn that ended normally does not.
func TestAudit2ResumeAfterACutOffTurnSaysSo(t *testing.T) {
	const notice = "[eagent] You were restarted in the middle of a turn."
	notices := func(evs []event.Event) int {
		n := 0
		for _, ev := range evs {
			if ev.Type == event.HarnessMessage {
				var d event.HarnessMessageData
				_ = ev.Decode(&d)
				if strings.HasPrefix(d.Text, notice) {
					n++
				}
			}
		}
		return n
	}
	t.Run("cut off", func(t *testing.T) {
		hang := make(chan struct{})
		t.Cleanup(func() { close(hang) })
		var resumed atomic.Bool
		s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
			if resumed.Load() {
				return hold()
			}
			if msgs[len(msgs)-1]["role"] == "tool" {
				return reply{block: hang, calls: hold().calls}
			}
			if strings.Contains(lastUserText(msgs), "kept for you") {
				return reply{calls: []event.ToolCall{tc("relay_screen", `{}`)}}
			}
			return hold()
		}, nil)
		s.type_("@e what is on the screen?")
		s.waitFor("the second call of the turn", func() bool { return s.model.count(testModel) == 2 })
		path, project := s.rt.sess.Path, s.rt.opts.Project
		s.stop()
		evs := mustReadCore(t, path)
		cut := false
		for _, ev := range evs {
			var d event.TurnData
			if ev.Type == event.TurnEnd && ev.Decode(&d) == nil && d.Reason == "interrupted" {
				cut = true
			}
		}
		if !cut {
			t.Fatal("the turn was not recorded as interrupted")
		}
		resumed.Store(true)
		s2, _ := coreResume(t, path, project, s.model, newFakeSeat("ok"))
		s2.waitFor("the notice", func() bool { return notices(s2.events(event.HarnessMessage)) == 1 })
	})
	t.Run("ended normally", func(t *testing.T) {
		s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
			if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
				return say("Hello.")
			}
			return hold()
		}, nil)
		s.type_("@e hello")
		s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
		s.settled()
		path, project := s.rt.sess.Path, s.rt.opts.Project
		s.stop()
		s2, _ := coreResume(t, path, project, s.model, newFakeSeat("ok"))
		s2.settled()
		if n := notices(s2.events(event.HarnessMessage)); n != 0 {
			t.Fatalf("%d cut-off notices after a turn that ended normally", n)
		}
	})
}

// ---- 12. how a session with no terminal ends --------------------------------

// A session with no terminal ends awaiting-input (exit 2) when no agent
// could be started, stays up while its agent runs, and ends done (exit 0)
// when the agent was stopped with nothing open.
func TestAudit2EndReasons(t *testing.T) {
	endOf := func(t *testing.T, s *session) string {
		return state.Replay(mustReadCore(t, s.rt.sess.Path)).EndReason
	}
	t.Run("no agent could be started", func(t *testing.T) {
		model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
			if msgs[len(msgs)-1]["role"] == "tool" || strings.Contains(lastUserText(msgs), "The session is ending now") {
				return hold()
			}
			return say("I could not start Codex here: the launcher failed.")
		})
		s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, nil, model, nil)
		if code := coreExit(s); code != 2 {
			t.Fatalf("exit %d", code)
		}
		if r := endOf(t, s); r != "awaiting-input" {
			t.Fatalf("end reason %q", r)
		}
	})
	t.Run("an agent still running", func(t *testing.T) {
		model := audit2Model(t, func(string, []map[string]any) reply { return hold() })
		s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, newFakeSeat("Merged #42."), model, nil)
		s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
		select {
		case code := <-s.done:
			s.done <- code
			t.Fatalf("the session ended (%d) with its agent still running", code)
		case <-time.After(500 * time.Millisecond):
		}
	})
	t.Run("agent stopped with nothing open", func(t *testing.T) {
		model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
			if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
				return stopsAndReports("Codex merged #42 and #44; stopping it.")
			}
			return hold()
		})
		s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief + " Stop when done."}, newFakeSeat("Merged #42 and #44."), model, nil)
		if code := coreExit(s); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if r := endOf(t, s); r != "done" {
			t.Fatalf("end reason %q", r)
		}
	})
}

// ---- 13. shutdown and the closing word in flight ----------------------------

// Shutdown gives a closing word that is being written finalReportGrace,
// not the 5 seconds other calls get.
func TestAudit2ShutdownGivesTheClosingWordItsGrace(t *testing.T) {
	run := func(t *testing.T, block <-chan struct{}) (*session, chan struct{}) {
		started := make(chan struct{})
		var once sync.Once
		model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
			switch {
			case strings.Contains(lastUserText(msgs), "The session is ending now"):
				once.Do(func() { close(started) })
				r := say("Final: #42 and #44 are merged.")
				r.block = block
				return r
			case strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool":
				return stopsWhenDone()
			}
			return hold()
		})
		s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief + " Stop when done."}, newFakeSeat("Merged #42 and #44."), model, nil)
		return s, started
	}
	t.Run("a short grace is all it gets", func(t *testing.T) {
		old := finalReportGrace
		finalReportGrace = 300 * time.Millisecond
		t.Cleanup(func() { finalReportGrace = old })
		never := make(chan struct{})
		defer close(never) // before the model server is closed
		s, started := run(t, never)
		select {
		case <-started:
		case <-time.After(15 * time.Second):
			t.Fatal("no closing look")
		}
		at := time.Now()
		s.stop()
		if d := time.Since(at); d > 2500*time.Millisecond {
			t.Fatalf("shutdown took %s with a %s grace", d, finalReportGrace)
		}
		if !audit2Logged(s.ui, "a model call did not stop in time") {
			t.Fatalf("logs %q", s.ui.logged())
		}
	})
	t.Run("the word lands within the grace", func(t *testing.T) {
		old := finalReportGrace
		finalReportGrace = 4 * time.Second
		t.Cleanup(func() { finalReportGrace = old })
		slow := make(chan struct{})
		s, started := run(t, slow)
		select {
		case <-started:
		case <-time.After(15 * time.Second):
			t.Fatal("no closing look")
		}
		time.AfterFunc(800*time.Millisecond, func() { close(slow) })
		s.stop()
		msgs := audit2Messages(mustReadCore(t, s.rt.sess.Path))
		if len(msgs) != 1 || msgs[0].Text != "Final: #42 and #44 are merged." {
			t.Fatalf("messages %+v", msgs)
		}
	})
}

// ---- 14. old commands are not typed into the agent --------------------------

// /tasks, /procs and /kill typed while an agent runs are answered by eagent
// itself and never typed into the agent.
func TestAudit2OldCommandsAreAnsweredLocally(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	for _, cmd := range []string{"/tasks", "/procs", "/kill"} {
		s.type_(cmd)
		s.waitFor(cmd+" to be answered", func() bool { return audit2Logged(s.ui, cmd+" went with eagent's own tasks and processes") })
	}
	s.settled()
	if sent := s.seat.sentTexts(); len(sent) != 0 {
		t.Fatalf("typed into the agent: %q", sent)
	}
	if n := len(s.events(event.UserMessage)); n != 0 {
		t.Fatalf("%d user messages recorded", n)
	}
}

// ---- 15. the last title and summary do not hold the end ---------------------

// The last inline title and summary call of a session that is ending is
// bounded by summaryFinalWait: a model that hangs on it does not hold the
// end for longer.
func TestAudit2FinalSummaryCallIsBounded(t *testing.T) {
	coreSummariesOn(t, false)
	old := summaryFinalWait
	summaryFinalWait = 300 * time.Millisecond
	t.Cleanup(func() { summaryFinalWait = old })
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	var asked atomic.Int64 // when the thread-only call started (unix nanos)
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "this look is only about the thread's title and summary"):
			asked.CompareAndSwap(0, time.Now().UnixNano())
			return reply{calls: hold().calls, block: never}
		case strings.Contains(tail, "finished a turn"):
			return stopsAndReports("Codex merged #42 and #44; stopping it.")
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief + " Stop when done."}, newFakeSeat("Merged #42 and #44."), model, nil)
	code := coreExit(s)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	start := asked.Load()
	if start == 0 {
		t.Fatal("the last title and summary were never asked for")
	}
	evs := mustReadCore(t, s.rt.sess.Path)
	end := evs[len(evs)-1]
	if end.Type != event.SessionEnd {
		t.Fatalf("the log ends with %s", end.Type)
	}
	if d := end.Time.Sub(time.Unix(0, start)); d > 3*time.Second {
		t.Fatalf("the hanging summary call held the end for %s", d)
	}
}

// ---- 16. what ask_user says -----------------------------------------------

// ask_user's result says what becomes of the question: with nobody at a
// terminal and no phone the session ends now and is answered with eagent
// resume --answer; at a terminal the answer comes as a message.
func TestAudit2AskUserResultSaysWhatHappensNext(t *testing.T) {
	t.Run("no terminal, no phone", func(t *testing.T) {
		rt, _, _ := batchExit(t, 2, brief, "Merged #42; #45 conflicts.", func(_ string, msgs []map[string]any) reply {
			if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
				return phoneAskAndStop("Rebase #45 or leave it?", "rebase", "leave")
			}
			return hold()
		})
		res := audit2Results(mustReadCore(t, rt.sess.Path), "ask_user")
		if len(res) != 1 || !strings.Contains(res[0].Output, "this session ends now") || !strings.Contains(res[0].Output, "`eagent resume --answer`") {
			t.Fatalf("result %+v", res)
		}
	})
	t.Run("at a terminal", func(t *testing.T) {
		s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
			if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
				return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which branch?"}`)}}
			}
			return hold()
		}, nil)
		s.type_("@e ask me which branch")
		s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
		s.settled()
		res := audit2Results(s.events(event.ToolResult), "ask_user")
		if len(res) != 1 || !strings.Contains(res[0].Output, "the answer will appear as a message from the user") {
			t.Fatalf("result %+v", res)
		}
	})
}

// ---- 17. a picture already seen --------------------------------------------

// view_image of a picture already in the conversation whose file has not
// changed sends no image again; once the file changes it is sent again.
func TestAudit2ViewImageIsNotSentTwiceUntilTheFileChanges(t *testing.T) {
	var project atomic.Pointer[string]
	bigger := audit2PNG(t, 4, 3)
	var mu sync.Mutex
	looks := 0
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		called := audit2Called(msgs)
		afterLook := len(called) == 1 && called[0] == "view_image"
		switch {
		case afterLook && looks == 3:
			looks++
			return say("Seen it.")
		case afterLook && looks == 2:
			if err := os.WriteFile(filepath.Join(*project.Load(), "shot.png"), bigger, 0o644); err != nil {
				t.Error(err)
			}
			fallthrough
		case afterLook && looks < 3, called == nil && strings.Contains(lastUserText(msgs), "kept for you") && looks == 0:
			looks++
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"shot.png"}`)}}
		}
		return hold()
	}, nil)
	t.Cleanup(func() {
		if t.Failed() {
			for _, d := range audit2Results(s.events(event.ToolResult), "view_image") {
				t.Logf("view_image: %s (%d images)", d.Output, len(d.Images))
			}
		}
	})
	p := s.rt.opts.Project
	project.Store(&p)
	if err := os.WriteFile(filepath.Join(p, "shot.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	s.type_("@e look at shot.png")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	views := audit2Results(s.events(event.ToolResult), "view_image")
	if len(views) != 3 {
		t.Fatalf("%d looks", len(views))
	}
	if len(views[0].Images) != 1 || len(views[1].Images) != 0 || !strings.Contains(views[1].Output, "unchanged since you looked at it") || len(views[2].Images) != 1 || views[2].Images[0].Width != 4 {
		t.Fatalf("views = %+v", views)
	}
}

// ---- 18-23. the dossier -------------------------------------------------------

// What set a restart off and had not been looked at yet is shown whole in
// the new conversation, not left to the briefing.
func TestAudit2DossierShowsWhatSetItOffWhole(t *testing.T) {
	const marker = "END-OF-THE-LONG-MESSAGE-7731"
	long := "@e " + strings.Repeat("here is the whole changelog for the release, line after line. ", 40) + marker
	if len(long) <= 1500 {
		t.Fatal("the message must be longer than the dossier's 1500-character quote")
	}
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var first atomic.Bool
	var mu sync.Mutex
	var after []map[string]any
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			return reply{text: briefing(places(ask)[:3])}
		}
		if len(msgs) > 1 {
			if open, _ := msgs[1]["content"].(string); strings.HasPrefix(open, "[Briefing.") {
				mu.Lock()
				if after == nil {
					after = msgs
				}
				mu.Unlock()
			}
		}
		if first.CompareAndSwap(false, true) {
			r := hold()
			r.prompt, r.block = 25000, release
			return r
		}
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			return say("Noted.")
		}
		return hold()
	}, nil)
	s.waitFor("the first look", func() bool { return s.model.count(testModel) == 1 })
	s.type_(long)
	s.waitFor("the long message", func() bool {
		for _, ev := range s.events(event.UserMessage) {
			if strings.Contains(string(ev.Data), marker) {
				return true
			}
		}
		return false
	})
	close(release)
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	s.waitFor("a call after the restart", func() bool { mu.Lock(); defer mu.Unlock(); return after != nil })
	var seq int64
	for _, ev := range s.events(event.UserMessage) {
		if strings.Contains(string(ev.Data), marker) {
			seq = ev.Seq
		}
	}
	d := s.dossiers()[0]
	if d.CoversSeq >= seq {
		t.Fatalf("the dossier covers %d, past the message that set it off (%d)", d.CoversSeq, seq)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(allText(after), marker) {
		t.Fatal("the first call after the restart does not show the message whole")
	}
}

// audit2Dossier starts a session that writes one dossier, with write
// answering every call of the dossier pass. The dossier is set off by the
// next look, which reports a prompt over the threshold.
func audit2Dossier(t *testing.T, write func(n int, msgs []map[string]any) reply) (*session, func() []map[string]any) {
	t.Helper()
	var big atomic.Bool
	var mu sync.Mutex
	n := 0
	var after []map[string]any
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if dossierRequest(msgs) != "" {
			mu.Lock()
			n++
			k := n
			mu.Unlock()
			return write(k, msgs)
		}
		if len(msgs) > 1 {
			if open, _ := msgs[1]["content"].(string); strings.HasPrefix(open, "[Briefing.") {
				mu.Lock()
				if after == nil {
					after = msgs
				}
				mu.Unlock()
			}
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			r = say("Hello.")
		}
		if big.CompareAndSwap(true, false) {
			r.prompt = 25000
		}
		return r
	}, nil)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	big.Store(true)
	s.type_("@e hello")
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	return s, func() []map[string]any { mu.Lock(); defer mu.Unlock(); return after }
}

// A briefing that still cites fewer than three places after being sent
// back once is kept, labelled as no map, and the next conversation is told
// it has no usable MAP.
func TestAudit2DossierWithoutAMapIsLabelled(t *testing.T) {
	s, after := audit2Dossier(t, func(int, []map[string]any) reply { return reply{text: briefing(nil)} })
	d := s.dossiers()[0]
	if d.By != "eagent" || !d.NoMap || !strings.HasPrefix(d.Text, "(eagent checked this briefing and it is not a map") {
		t.Fatalf("dossier by %q nomap=%v:\n%s", d.By, d.NoMap, d.Text)
	}
	s.type_("@e are you there?")
	s.waitFor("a call after the restart", func() bool { return after() != nil })
	open, _ := after()[1]["content"].(string)
	if !strings.Contains(open, "has no usable MAP") {
		t.Fatalf("the briefing notice does not say there is no map:\n%s", open[:min(len(open), 600)])
	}
}

// One failed model call during the dossier pass is made again, and the
// model's briefing is kept.
func TestAudit2DossierRetriesAFailedCall(t *testing.T) {
	old := dossierErrorPause
	dossierErrorPause = 10 * time.Millisecond
	t.Cleanup(func() { dossierErrorPause = old })
	s, _ := audit2Dossier(t, func(n int, msgs []map[string]any) reply {
		if n == 1 {
			return reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}
		}
		return reply{text: briefing(places(dossierRequest(msgs))[:3])}
	})
	if d := s.dossiers()[0]; d.By != "eagent" {
		t.Fatalf("dossier by %q after one failed call", d.By)
	}
	if !audit2Logged(s.ui, "dossier: model call failed") {
		t.Fatalf("logs %q", s.ui.logged())
	}
}

// The dossier pass is bounded in time: past it the writer is told the
// lookup was its last, and one that looks things up again gets the
// harness's briefing.
func TestAudit2DossierPassIsBoundedInTime(t *testing.T) {
	old := dossierPassTime
	dossierPassTime = 0
	t.Cleanup(func() { dossierPassTime = old })
	var mu sync.Mutex
	var told string
	s, _ := audit2Dossier(t, func(n int, msgs []map[string]any) reply {
		if n == 2 {
			mu.Lock()
			told = lastText(msgs)
			mu.Unlock()
		}
		return reply{calls: []event.ToolCall{tc("session_search", `{"query":"PR"}`)}}
	})
	if d := s.dossiers()[0]; d.By != "harness" {
		t.Fatalf("dossier by %q", d.By)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(told, "That was the last lookup") {
		t.Fatalf("the writer was not told to stop looking:\n%s", told)
	}
}

// The facts a dossier carries list every user message: the first 3 and the
// latest 25 word for word, each one between in a line with its start, and
// past 150 between the oldest of those by place only. Each agent's model
// and effort are on its line.
func TestAudit2DossierFactsListEveryUserMessage(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), RelayModel: "gpt-test", RelayEffort: "high"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.sess.Close() })
	const total = 3 + 160 + 25
	text := func(i int) string {
		return fmt.Sprintf("msg-%03d ", i) + strings.Repeat("x", 300) + fmt.Sprintf(" END-%03d", i)
	}
	var evs []event.Event
	for i := 0; i < total; i++ {
		evs = append(evs, rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text(i)})))
	}
	facts := rt.dossierFacts(rt.st.LastSeq(), "")
	if !strings.Contains(facts, fmt.Sprintf("(%d in all: the first 3 and the latest 25 word for word", total)) {
		t.Fatalf("no header for a long list:\n%s", facts[:min(len(facts), 2000)])
	}
	if !strings.Contains(facts, "; model gpt-test, effort high") {
		t.Fatalf("the agent's line lacks its model and effort:\n%s", facts[:min(len(facts), 2000)])
	}
	whole := func(i int) bool { return strings.Contains(facts, text(i)) }
	for _, i := range []int{0, 1, 2, total - 25, total - 1} {
		if !whole(i) {
			t.Fatalf("message %d is not word for word", i)
		}
	}
	// 160 between: the oldest 10 by place, the next 150 in a line each.
	var lines []string
	for i := 3; i < 13; i++ {
		if strings.Contains(facts, fmt.Sprintf("msg-%03d ", i)) {
			t.Fatalf("message %d should be given by place only", i)
		}
		lines = append(lines, fmt.Sprint(evs[i].Source.Line))
	}
	want := "- by place only, " + evs[3].Source.File + " lines " + strings.Join(lines, ", ")
	if !strings.Contains(facts, want) {
		t.Fatalf("facts lack %q:\n%s", want, facts[:min(len(facts), 3000)])
	}
	for _, i := range []int{13, 80, total - 26} {
		if !strings.Contains(facts, fmt.Sprintf("msg-%03d xxx", i)) || whole(i) {
			t.Fatalf("message %d between should have one clipped line", i)
		}
	}
}

// The notice that the conversation is full right after a restart is told
// once per subsession, even across a resume.
func TestAudit2FullNoticeIsToldOncePerSubsession(t *testing.T) {
	var big atomic.Bool
	brain := func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			return reply{text: briefing(places(ask)[:3])}
		}
		r := hold()
		tail := lastUserText(msgs)
		if msgs[len(msgs)-1]["role"] != "tool" && (strings.Contains(tail, "kept for you") || strings.Contains(tail, "finished a turn")) {
			r = say("Noted.")
		}
		if big.CompareAndSwap(true, false) {
			r.prompt = 25000
		}
		return r
	}
	s := startSession(t, brief, brain, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() >= 1 })
	s.settled()
	big.Store(true)
	s.type_("@e one")
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	s.settled()
	big.Store(true)
	s.type_("@e two")
	told := func(ui *fakeUI) int {
		n := 0
		for _, m := range ui.said() {
			if strings.HasPrefix(m, fullNotice) {
				n++
			}
		}
		return n
	}
	s.waitFor("the notice", func() bool { return told(s.ui) == 1 })
	s.settled()
	before := s.ui.messageCount()
	s.type_("@e three") // a small prompt: the resumed run starts below the threshold
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == before+1 })
	s.settled()
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()

	s2, _ := coreResume(t, path, project, s.model, newFakeSeat("ok"))
	big.Store(true)
	s2.type_("@e four")
	s2.waitFor("the check after the first call", func() bool { return audit2Logged(s2.ui, "the conversation is too long right after a restart") })
	s2.settled()
	if n := told(s2.ui); n != 0 {
		t.Fatalf("the notice was told again after the resume (%d)", n)
	}
	s2.stop()
	n := 0
	for _, m := range audit2Messages(mustReadCore(t, path)) {
		if strings.HasPrefix(m.Text, fullNotice) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the notice is in the record %d times", n)
	}
}

// ---- 24-26. the phone and the resume record --------------------------------

// The phone's status line: a model call in flight is "thinking", a long one
// says so, writing a dossier says what it is for, and an agent stopped on a
// prompt is waiting on the user.
func TestAudit2StatusWording(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	check := func(set func(t *turnState), text, kind string) {
		t.Helper()
		var gotText, gotKind string
		s.rt.sync(func() {
			saved := s.rt.turns
			set(&s.rt.turns)
			a := s.rt.currentActivity()
			gotText, gotKind = a.Text, a.Kind
			s.rt.turns.busy, s.rt.turns.callAt, s.rt.turns.compacting = saved.busy, saved.callAt, saved.compacting
		})
		if gotText != text || gotKind != kind {
			t.Fatalf("status %q (%s), want %q (%s)", gotText, gotKind, text, kind)
		}
	}
	check(func(t *turnState) { t.busy, t.callAt = true, time.Now() }, "Thinking about the next step", "thinking")
	check(func(t *turnState) { t.busy, t.callAt = true, time.Now().Add(-50*time.Second) }, "Still thinking (a long think or a long reply)", "thinking")
	check(func(t *turnState) { t.busy, t.compacting = true, true }, "Rereading the session so far, to carry on with a clear head", "thinking")
	s.seat.emit(relay.Event{Kind: relay.KindState, State: "blocked", Detail: "Do you trust this folder? 1. Yes 2. No"})
	s.waitFor("the agent to be blocked", func() bool {
		blocked := false
		s.read(func(st *state.State) { blocked = st.Relays["codex"] != nil && st.Relays["codex"].State == "blocked" })
		return blocked
	})
	s.settled()
	check(func(t *turnState) { t.busy = false }, "Codex is stopped on a prompt on its screen", "waiting")
}

// With the phone on, the tail for an agent's turn end asks for the message
// to be marked important when the work is finished; without one it does not.
func TestAudit2TurnEndTailAsksForTheBuzzOnlyWithAPhone(t *testing.T) {
	const buzz = "mark your message important so their phone buzzes"
	t.Run("phone", func(t *testing.T) {
		var seen tails
		g := phoneStart(t, newPhoneFake(false), brief, true, func(_ string, msgs []map[string]any) reply {
			seen.add(lastUserText(msgs))
			return hold()
		}, nil)
		waitFor(t, "the turn-end look", func() bool { return seen.find("finished a turn") != "" })
		_ = g
		if !strings.Contains(seen.find("finished a turn"), buzz) {
			t.Fatalf("the tail lacks the buzz:\n%s", seen.find("finished a turn"))
		}
	})
	t.Run("no phone", func(t *testing.T) {
		var seen tails
		s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
			seen.add(lastUserText(msgs))
			return hold()
		}, nil)
		s.waitFor("the turn-end look", func() bool { return seen.find("finished a turn") != "" })
		if strings.Contains(seen.find("finished a turn"), buzz) {
			t.Fatal("the tail asks for a buzz with no phone")
		}
	})
}

// A resume records a route event for eagent when the model in use differs
// from the one the record names, and none when it is the same.
func TestAudit2ResumeRecordsAChangedModel(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()
	routes := func() []event.RouteData {
		var out []event.RouteData
		for _, ev := range mustReadCore(t, path) {
			if ev.Type == event.Route {
				var d event.RouteData
				_ = ev.Decode(&d)
				out = append(out, d)
			}
		}
		return out
	}
	cfg := testConfig(s.model.srv.URL)
	cfg.Model.Model = "eagent-model-2"
	s2 := audit2Resume(t, cfg, Options{Project: project, Interactive: true}, path, newFakeSeat("ok"), s.model)
	s2.settled()
	s2.stop()
	r := routes()
	if len(r) != 1 || r[0].Actor != event.ActorEagent || r[0].Model != "eagent-model-2" || r[0].Reason != "the session was resumed with this model" {
		t.Fatalf("routes after a resume on another model: %+v", r)
	}
	s3 := audit2Resume(t, cfg, Options{Project: project, Interactive: true}, path, newFakeSeat("ok"), s.model)
	s3.settled()
	s3.stop()
	if r := routes(); len(r) != 1 {
		t.Fatalf("routes after a resume on the same model: %+v", r)
	}
}

// ---- 27. an old session is taken over once ---------------------------------

// A log of the three-actor design that was taken over (here with the
// harness's own briefing) is not taken over again on the next resume.
func TestAudit2OldSessionIsTakenOverOnce(t *testing.T) {
	project, sess, _ := oldSession(t)
	s, _ := resumeOld(t, project, sess, func(_ string, msgs []map[string]any) reply {
		if dossierRequest(msgs) != "" {
			return reply{text: "ok"}
		}
		if r, ok := startsAnAgent(msgs); ok {
			return r
		}
		return hold()
	})
	s.waitFor("the message to reach a new agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	if d := s.dossiers(); len(d) != 2 || d[1].By != "harness" {
		t.Fatalf("dossiers %+v", d)
	}
	upgraded := false
	s.read(func(st *state.State) { upgraded = st.Upgraded() })
	if !upgraded {
		t.Fatal("the session does not count as upgraded")
	}
	path := s.rt.sess.Path
	s.stop()
	s2, _ := coreResume(t, path, project, s.model, newFakeSeat("ok"))
	s2.settled()
	s2.stop()
	evs := mustReadCore(t, path)
	st := state.Replay(evs)
	n := 0
	for _, sub := range st.Subsessions {
		if sub.Reason == "upgrade" {
			n++
		}
	}
	dossiers := 0
	for _, ev := range evs {
		if ev.Type == event.Dossier {
			dossiers++
		}
	}
	if n != 1 || dossiers != 2 {
		t.Fatalf("after a second resume: %d upgrade subsessions, %d dossiers", n, dossiers)
	}
}
