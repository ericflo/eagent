package harness

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
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

// These tests hold the behaviours the three-actor eagent had that the one
// loop keeps: each was dropped or weakened once during the rewrite and put
// back. One test per behaviour, named for what the user relies on.

// lastText is the text of the last message of a prompt, whoever wrote it:
// what was sent back to the model arrives as a tool result when its reply
// called a tool.
func lastText(msgs []map[string]any) string {
	if len(msgs) == 0 {
		return ""
	}
	return allText(msgs[len(msgs)-1:])
}

// tails collects the last user message of every call, for tests that check
// what the model was told.
type tails struct {
	mu   sync.Mutex
	list []string
}

func (t *tails) add(s string) { t.mu.Lock(); t.list = append(t.list, s); t.mu.Unlock() }
func (t *tails) find(sub string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.list) - 1; i >= 0; i-- {
		if strings.Contains(t.list[i], sub) {
			return t.list[i]
		}
	}
	return ""
}

// batch runs a session with no terminal and returns when it has ended.
func batch(t *testing.T, prompt, agentReply string, brain func(model string, msgs []map[string]any) reply) (*Runtime, *fakeUI, *fakeSeat) {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat(agentReply)
	useFakeRelays(t, seat)
	model := newScripted(brain)
	t.Cleanup(model.srv.Close)
	ui := &fakeUI{}
	rt, err := New(testConfig(model.srv.URL), Options{Project: t.TempDir(), Prompt: prompt}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	})
	select {
	case code := <-done:
		done <- code
		if code != 0 {
			t.Fatalf("exit code %d; logs %v", code, ui.logged())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("the session did not end; said %q; logs %v", ui.said(), ui.logged())
	}
	return rt, ui, seat
}

// stopsWhenDone is the reply that stops the agent without telling the user
// anything: what a batch session's end has to make up for.
func stopsWhenDone() reply {
	return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"the work is done"}`)}}
}

// A message the user addressed to eagent is always answered. A model that
// holds is told once that holding is not an answer; if it still says
// nothing it is woken again for that message alone until it has answered.
func TestAMessageKeptForEagentIsNeverLeftUnanswered(t *testing.T) {
	old := owedRetryDelays
	owedRetryDelays = []time.Duration{30 * time.Millisecond, 30 * time.Millisecond, 30 * time.Millisecond}
	defer func() { owedRetryDelays = old }()
	var mu sync.Mutex
	reminded, woken := 0, 0
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(lastText(msgs), "Holding is not an answer"):
			reminded++
		case strings.Contains(lastUserText(msgs), "has had no answer"):
			woken++
			if woken == 2 {
				return say("Codex is idle; nothing has been asked of it yet.")
			}
		}
		return hold()
	}, nil)
	s.type_("@e what is Codex up to?")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	mu.Lock()
	r, w := reminded, woken
	mu.Unlock()
	if r < 1 || w != 2 {
		t.Fatalf("reminded %d times in the first call's turn, woken again %d times", r, w)
	}
	var owed int64
	s.rt.sync(func() { owed = s.rt.turns.owed })
	if owed != 0 {
		t.Fatal("the message is still counted as unanswered after the answer")
	}
	// Answered: nothing wakes the model for it again.
	base := s.model.count(testModel)
	time.Sleep(150 * time.Millisecond)
	if got := s.model.count(testModel); got != base {
		t.Fatalf("%d more calls after the answer", got-base)
	}
}

// After a message has been typed into the agent's terminal, the model is
// asked for one short line saying it got there.
func TestAPassedMessageIsAcknowledgedInOneLine(t *testing.T) {
	var seen tails
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		seen.add(tail)
		if strings.Contains(tail, "Say in one short line that it reached Codex") {
			return say("Sent to Codex.")
		}
		return hold()
	}, nil)
	s.waitFor("the acknowledgement", func() bool { return s.ui.messageCount() >= 1 })
	tail := seen.find("Say in one short line that it reached Codex")
	for _, want := range []string{"typed into Codex's terminal as written", "do not repeat the message back or answer it", "Hold instead if Codex has already answered it"} {
		if !strings.Contains(tail, want) {
			t.Fatalf("the tail after a passed message lacks %q:\n%s", want, tail)
		}
	}
}

// A reply the output limit cut off before any tool call is sent back for
// another go instead of being taken for silence.
func TestAReplyCutOffByTheOutputLimitIsSentBack(t *testing.T) {
	var mu sync.Mutex
	cut := 0
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(lastText(msgs), "cut off by the output limit"):
			cut++
			return say("Codex merged #42 and #44.")
		case strings.Contains(lastUserText(msgs), "finished a turn"):
			return reply{text: "Codex merged #42 and #44, and then it went on to", finish: "length"}
		}
		return hold()
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	mu.Lock()
	defer mu.Unlock()
	if cut != 1 {
		t.Fatalf("sent back %d times", cut)
	}
	if got := s.ui.said()[0]; got != "Codex merged #42 and #44." {
		t.Fatalf("said %q", got)
	}
	if a := s.events(event.Assistant); len(a) != 1 {
		t.Fatalf("%d replies recorded, want only the second", len(a))
	}
}

// A new question takes the place of one still open.
func TestANewQuestionReplacesTheOpenOne(t *testing.T) {
	var mu sync.Mutex
	turns := 0
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if !strings.Contains(lastUserText(msgs), "finished a turn") || msgs[len(msgs)-1]["role"] == "tool" {
			return hold()
		}
		mu.Lock()
		defer mu.Unlock()
		turns++
		if turns == 1 {
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge #45 as well?","options":["yes","no"]}`)}}
		}
		return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"#45 conflicts with main. Rebase it or leave it?"}`)}}
	}, nil)
	s.waitFor("the first question", func() bool { return s.ui.askedCount() == 1 })
	s.settled()
	s.seat.emit(
		relay.Event{Kind: relay.KindState, State: "working"},
		relay.Event{Kind: relay.KindTurn, Text: "#45 has a conflict with main.", DurationMS: 1000},
		relay.Event{Kind: relay.KindState, State: "idle"},
	)
	s.waitFor("the second question", func() bool { return s.ui.askedCount() == 2 })
	s.settled()
	var open string
	s.read(func(st *state.State) {
		if st.Question != nil {
			open = st.Question.Text
		}
	})
	if open != "#45 conflicts with main. Rebase it or leave it?" {
		t.Fatalf("the open question is %q", open)
	}
	for _, ev := range s.events(event.ToolResult) {
		var d event.ToolResultData
		_ = ev.Decode(&d)
		if d.IsError {
			t.Fatalf("a question was refused: %s", d.Output)
		}
	}
}

// One wake may take many calls (clearing a dialog takes several). When the
// limit stops it with the agent still stuck, eagent gets one more go, and
// after that the user is told how to reach the terminal.
func TestAStuckAgentGetsAnotherGoAfterTheCallLimitThenTheUserIsTold(t *testing.T) {
	if got := config.Defaults().MaxCallsPerTurn; got != 30 {
		t.Fatalf("the default call limit is %d, want 30 as before", got)
	}
	var seen tails
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		seen.add(allText(msgs))
		if msgs[len(msgs)-1]["role"] == "tool" || strings.Contains(allText(msgs), "stuck on something on its screen") {
			return reply{calls: []event.ToolCall{tc("relay_screen", `{}`)}}
		}
		return hold()
	}, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 3 })
	s.type_("start on the release")
	s.waitFor("the message to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	s.seat.emit(relay.Event{Kind: relay.KindState, State: "blocked", Detail: "Do you trust the files in this folder?"})
	s.waitFor("the user to be told", func() bool {
		for _, m := range s.ui.said() {
			if strings.Contains(m, "I could not clear it") {
				return true
			}
		}
		return false
	})
	s.settled()
	if seen.find("You were stopped after 3 calls in a row, and Codex is still stuck on its screen") == "" {
		t.Fatal("eagent was not given another go after the call limit")
	}
	ends := 0
	for _, ev := range s.events(event.TurnEnd) {
		var d event.TurnData
		_ = ev.Decode(&d)
		if d.Reason == "call limit" {
			ends++
		}
	}
	if ends != 2 {
		t.Fatalf("%d turns ended at the call limit, want 2 (the first and the one more go)", ends)
	}
	told := 0
	for _, m := range s.ui.said() {
		if strings.Contains(m, "eagent relays attach") {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("the user was told %d times", told)
	}
}

// A session with no terminal ends with a closing word, marked important. A
// model that holds when the user has heard nothing is told to speak.
func TestSessionWithNoTerminalEndsWithAClosingWord(t *testing.T) {
	var seen tails
	rt, ui, _ := batch(t, brief+" Stop when done.", "Done: #42 and #44 are merged.", func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		seen.add(tail)
		switch {
		case strings.Contains(lastText(msgs), "the user has heard nothing from you. Send the final report now"):
			return say("All done: #42 and #44 are merged on main; nothing is left open.")
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "finished a turn"):
			return stopsWhenDone()
		}
		return hold()
	})
	if said := ui.said(); len(said) != 1 || !strings.HasPrefix(said[0], "All done:") {
		t.Fatalf("said %q", said)
	}
	if seen.find("The session is ending now") == "" || seen.find("The user has heard nothing from you yet. Send a complete final report") == "" {
		t.Fatal("the model was not asked for the closing word")
	}
	evs, _ := store.Read(rt.sess.Path)
	for _, ev := range evs {
		if ev.Type == event.Message {
			var d event.MessageData
			_ = ev.Decode(&d)
			if !d.Important {
				t.Fatal("the closing word is not marked important")
			}
		}
	}
	if st := state.Replay(evs); !st.Ended || st.EndReason != "done" {
		t.Fatalf("ended=%v reason=%q", st.Ended, st.EndReason)
	}
}

// When the user has already heard how it ended, the model may hold on the
// closing look and nothing is repeated.
func TestClosingWordIsNotRepeated(t *testing.T) {
	var seen tails
	_, ui, _ := batch(t, brief+" Stop when done.", "Done: #42 and #44 are merged.", func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		seen.add(tail)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "finished a turn"):
			return reply{calls: []event.ToolCall{
				tc("send_message", `{"text":"Codex merged #42 and #44 on main; stopping it as you asked.","important":true}`),
				tc("relay_control", `{"action":"stop","reason":"the work is done"}`),
			}}
		}
		return hold()
	})
	if said := ui.said(); len(said) != 1 {
		t.Fatalf("said %q", said)
	}
	if seen.find("If your last message already covers the final state") == "" {
		t.Fatal("the model was not given the closing look")
	}
}

// If the model will not give the closing word, eagent closes in its own
// words with the agent's last report.
func TestClosingWordFallsBackToTheHarness(t *testing.T) {
	_, ui, _ := batch(t, brief+" Stop when done.", "Done: #42 and #44 are merged.", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
			return stopsWhenDone()
		}
		return hold()
	})
	said := ui.said()
	if len(said) != 1 || !strings.Contains(said[0], "I could not write a proper closing report") || !strings.Contains(said[0], "Done: #42 and #44 are merged.") {
		t.Fatalf("said %q", said)
	}
}

// With the model unreachable, nothing running and nobody at a terminal,
// the session still ends, and still with a closing word.
func TestSessionEndsWithAClosingWordWhenTheModelIsDown(t *testing.T) {
	old := errorRetryDelays
	errorRetryDelays = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
	defer func() { errorRetryDelays = old }()
	var mu sync.Mutex
	down := false
	_, ui, _ := batch(t, brief+" Stop when done.", "Done: #42 and #44 are merged.", func(_ string, msgs []map[string]any) reply {
		mu.Lock()
		defer mu.Unlock()
		if down {
			return reply{status: 400, body: `{"error":{"message":"bad request shape","type":"invalid_request_error"}}`}
		}
		if strings.Contains(lastUserText(msgs), "finished a turn") {
			down = true
			return stopsWhenDone()
		}
		return hold()
	})
	said := strings.Join(ui.said(), "\n---\n")
	if !strings.Contains(said, "I could not write a proper closing report") || !strings.Contains(said, "Done: #42 and #44 are merged.") {
		t.Fatalf("said %q", said)
	}
	if n := strings.Count(said, "I could not write a proper closing report"); n != 1 {
		t.Fatalf("closed %d times", n)
	}
}

var timerSet = regexp.MustCompile(`timer (s\d+) set`)

// eagent can set itself a timer. When it comes due eagent is woken with the
// note it left, and acts on it; a timer can be listed and cancelled.
func TestTimersWakeEagentWithItsNote(t *testing.T) {
	var seen tails
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		tail, all := lastUserText(msgs), allText(msgs)
		seen.add(all)
		asked := all[strings.LastIndex(all, "@e ")+1:] // the user's newest message to eagent, and what followed
		last := msgs[len(msgs)-1]["role"]
		switch {
		case last == "tool":
			return hold()
		case strings.Contains(tail, "A timer you set came due"):
			return reply{calls: []event.ToolCall{tc("relay_send", `{"text":"Deploy to staging now."}`)}}
		case strings.Contains(tail, "kept for you") && strings.Contains(asked, "in a moment"):
			return reply{calls: []event.ToolCall{tc("schedule", `{"spec":"in 300ms","note":"Tell Codex to deploy to staging."}`)}}
		case strings.Contains(tail, "kept for you") && strings.Contains(asked, "every hour"):
			return reply{calls: []event.ToolCall{tc("schedule", `{"spec":"1h","note":"Check that staging is still up."}`)}}
		case strings.Contains(tail, "kept for you") && strings.Contains(asked, "stop checking"):
			ids := timerSet.FindAllStringSubmatch(all, -1)
			return reply{calls: []event.ToolCall{tc("cancel_schedule", `{"schedule":"`+ids[len(ids)-1][1]+`"}`)}}
		}
		return hold()
	}, nil)
	s.type_("@e in a moment tell Codex to deploy to staging")
	s.waitFor("the note to reach the agent when the timer comes due", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	if got := s.seat.sentTexts()[0]; got != "Deploy to staging now." {
		t.Fatalf("the agent was sent %q", got)
	}
	woke := seen.find("A timer you set came due")
	if !strings.Contains(woke, "Timer s1 came due") || !strings.Contains(woke, "The note you left yourself:]\nTell Codex to deploy to staging.") {
		t.Fatalf("the wake does not carry the note:\n%s", woke)
	}
	var create event.ScheduleCreateData
	_ = s.events(event.ScheduleCreate)[0].Decode(&create)
	if create.ID != "s1" || create.Kind != "once" || create.Note != "Tell Codex to deploy to staging." || create.Next.IsZero() {
		t.Fatalf("create = %+v", create)
	}
	if n := len(s.events(event.ScheduleFire)); n != 1 {
		t.Fatalf("the timer came due %d times", n)
	}
	active := -1
	s.read(func(st *state.State) { active = len(st.ActiveSchedules()) })
	if active != 0 {
		t.Fatalf("%d timers still set after a one-shot came due", active)
	}

	// A repeating timer stays set, shows in /schedules, and can be cancelled.
	s.type_("@e and check staging every hour")
	s.waitFor("the repeating timer", func() bool { return len(s.events(event.ScheduleCreate)) == 2 })
	s.settled()
	s.type_("/schedules")
	s.waitFor("the list", func() bool {
		for _, l := range s.ui.logged() {
			if strings.Contains(l, `s2 loop "1h"`) && strings.Contains(l, "Check that staging is still up.") {
				return true
			}
		}
		return false
	})
	s.type_("@e stop checking staging")
	s.waitFor("the cancellation", func() bool { return len(s.events(event.ScheduleCancel)) == 1 })
	s.settled()
	if seen.find("1 timer(s) of yours are set") == "" {
		t.Fatal("the model is not told a timer is set")
	}
	s.read(func(st *state.State) { active = len(st.ActiveSchedules()) })
	var armed int
	s.rt.sync(func() { armed = len(s.rt.timers) })
	if active != 0 || armed != 0 {
		t.Fatalf("after cancelling: %d set, %d armed", active, armed)
	}
}

// A session with a timer set does not end, and a resumed session arms its
// timers again.
func TestATimerKeepsTheSessionAliveAndSurvivesARestart(t *testing.T) {
	rt, ui, _ := batch(t, brief+" Then stop.", "Done: #42 and #44 are merged.", func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case msgs[len(msgs)-1]["role"] == "tool":
			return hold()
		case strings.Contains(tail, "A timer you set came due"):
			return say("An hour on, main is still green.")
		case strings.Contains(tail, "finished a turn"):
			return reply{calls: []event.ToolCall{
				tc("schedule", `{"spec":"in 400ms","note":"Tell the user whether main is still green."}`),
				tc("relay_control", `{"action":"stop","reason":"the work is done"}`),
			}}
		}
		return hold()
	})
	if said := ui.said(); len(said) != 1 || said[0] != "An hour on, main is still green." {
		t.Fatalf("said %q", said)
	}
	evs, _ := store.Read(rt.sess.Path)
	var fired, ended int64
	for _, ev := range evs {
		switch ev.Type {
		case event.ScheduleFire:
			fired = ev.Seq
		case event.SessionEnd:
			ended = ev.Seq
		}
	}
	if fired == 0 || ended < fired {
		t.Fatalf("the session ended (seq %d) before its timer came due (seq %d)", ended, fired)
	}

	// Resumed with a timer still set: it is armed again.
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			return reply{calls: []event.ToolCall{tc("schedule", `{"spec":"6h","note":"Look at the nightly build."}`)}}
		}
		return hold()
	}, nil)
	s.type_("@e look at the nightly build every six hours")
	s.waitFor("the timer", func() bool { return len(s.events(event.ScheduleCreate)) == 1 })
	s.settled()
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
	s2.waitFor("the timer to be armed again", func() bool {
		armed := false
		rt2.sync(func() { armed = rt2.timers["s1"] != nil })
		return armed
	})
}

// eagent can look at a picture: the image follows the tool's result in its
// conversation, and stays there on later calls.
func TestViewImageShowsThePictureToTheModel(t *testing.T) {
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		switch {
		case msgs[len(msgs)-1]["role"] == "tool" || strings.Contains(lastUserText(msgs), "image from view_image"):
			return say("The screenshot shows a green build badge.")
		case strings.Contains(lastUserText(msgs), "kept for you"):
			return reply{calls: []event.ToolCall{tc("view_image", `{"path":"shots/build.png"}`)}}
		}
		return hold()
	}, nil)
	dir := filepath.Join(s.rt.opts.Project, "shots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	s.type_("@e what does shots/build.png show?")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	var res event.ToolResultData
	_ = s.events(event.ToolResult)[0].Decode(&res)
	if res.Name != "view_image" || res.IsError || len(res.Images) != 1 || res.Images[0].Width != 8 || res.Images[0].Height != 6 || res.Images[0].ContentType != "image/png" {
		t.Fatalf("result = %+v", res)
	}
	if !strings.HasPrefix(res.Images[0].Path, filepath.Join(s.rt.sess.Path, "attachments")) {
		t.Fatalf("the picture was not copied into the session: %s", res.Images[0].Path)
	}
	reqs := s.model.requests(testModel)
	shown := false
	for _, m := range reqs[len(reqs)-1].Messages {
		if bytes.Contains(m, []byte("image from view_image")) && bytes.Contains(m, []byte("data:image/png;base64,")) {
			shown = true
		}
	}
	if !shown {
		t.Fatal("the picture is not in the model's conversation")
	}
	// Not a picture: refused, with the reason.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a picture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.rt.viewImage("shots/notes.txt"); err == nil || !strings.Contains(err.Error(), "not an image you can look at") {
		t.Fatalf("a text file was accepted as a picture: %v", err)
	}
}

// While autopilot is on, the standing order is restated in full every time
// eagent is woken to act, with the rules for acting on it.
func TestAutopilotOrderIsRestatedAtEveryWake(t *testing.T) {
	var seen tails
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		seen.add(lastUserText(msgs))
		return hold()
	}, nil)
	s.waitFor("the brief to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	s.type_("/autopilot keep going until every PR is merged and the tests pass")
	s.waitFor("autopilot", func() bool { return len(s.events(event.RelayAutopilot)) == 1 })
	s.settled()
	s.seat.emit(
		relay.Event{Kind: relay.KindState, State: "working"},
		relay.Event{Kind: relay.KindTurn, Text: "Merged #45. Two PRs are left.", DurationMS: 1000, Tools: 2},
		relay.Event{Kind: relay.KindState, State: "idle"},
	)
	s.waitFor("a look at the turn's end", func() bool {
		return seen.find("The standing order: keep going until every PR is merged and the tests pass") != ""
	})
	tail := seen.find("The standing order:")
	for _, want := range []string{
		"When Codex ends a turn having stopped, with nothing of its own still running, send it on with relay_send toward what is still missing",
		"When it ends a turn waiting on work of its own",
		"send nothing: it wakes itself when that work reports",
		"You are woken if it stays idle for",
	} {
		if !strings.Contains(tail, want) {
			t.Fatalf("the tail lacks %q:\n%s", want, tail)
		}
	}
}

// When restarting the conversation cannot make room (the dossier and the
// standing instructions alone reach the limit), the user is told once what
// to change, and eagent is not restarted again and again.
func TestARestartThatCannotHelpIsToldToTheUserOnce(t *testing.T) {
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			return reply{text: briefing(places(ask)[:3]), prompt: 25000}
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") {
			r = say("Here.")
		}
		r.prompt = 25000 // always past rollover_tokens
		return r
	}, nil)
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	s.settled()
	s.type_("@e are you there?")
	s.waitFor("the answer and the notice", func() bool { return s.ui.messageCount() == 2 })
	s.settled()
	s.type_("@e and now?")
	s.waitFor("the second answer", func() bool { return s.ui.messageCount() == 3 })
	s.settled()
	told := 0
	for _, m := range s.ui.said() {
		if strings.Contains(m, "full again right after I restarted it") && strings.Contains(m, "rollover_tokens") && strings.Contains(m, "INSTRUCTIONS.md") {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("the user was told %d times: %q", told, s.ui.said())
	}
	if n := len(s.dossiers()); n != 1 {
		t.Fatalf("%d dossiers written; a restart that cannot help was repeated", n)
	}
}

// The system prompt carries the rules that were written for the narrator
// and the orchestrator and still hold for the one loop.
func TestSystemPromptKeepsTheRulesOfTheOldPrompts(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	system := s.rt.system
	for _, want := range []string{
		// what eagent does itself, and what it cannot
		"You have no shell and no file tools",
		"A message kept for you always gets an answer or an action from you",
		"one short line that it reached the agent is enough",
		// answering about the session
		"Answer at once with the real state, not reassurance",
		"correct it in one sentence, without inventing why",
		"Translate mechanics into outcomes",
		"Say what the briefing says as what your notes say",
		"A final report stands on its own",
		// the thread's title and summary
		"Cover the whole session, never only the latest exchange",
		"its name alone is not enough: say what is being done with it",
		"Never the project directory's name on its own",
		"Do not copy the user's request word for word",
		"is not waiting on the user",
		"Where it stands is about the work, never about the agent",
		"The plumbing between you stays out too",
		"No links unless a link is the result",
		// timers and pictures
		"`schedule` wakes you later with a note you leave yourself",
		"look at a picture it made or the user sent (`view_image`)",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
	// Said only to a session whose user reads on a phone.
	if !strings.Contains(s.rt.prompts.Text("EAGENT.md"), "a reply to a session that has finished brings it back") {
		t.Error("the phone section no longer says a reply brings a finished session back")
	}
	if strings.Contains(s.rt.prompts.Text("PERSONA.md"), "send your last message again") {
		t.Error("the persona still promises to resend a message, which nothing does")
	}
}

// Project instructions are framed as the agent's, to be passed on only
// when relevant.
func TestProjectInstructionsAreFramedAsTheAgents(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	project := t.TempDir()
	dir := filepath.Join(project, ".agents", "eagent")
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An override of a prompt the one loop no longer reads is reported.
	if err := os.WriteFile(filepath.Join(dir, "prompts", "NARRATOR.md"), []byte("Speak like a pirate."), 0o644); err != nil {
		t.Fatal(err)
	}
	useFakeRelays(t, newFakeSeat("ok"))
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	defer model.srv.Close()
	ui := &fakeUI{input: make(chan string, 1)}
	cfg := testConfig(model.srv.URL)
	cfg.Instructions = "Always run make check before a commit." // config.Load reads it from .agents/eagent/INSTRUCTIONS.md
	rt, err := New(cfg, Options{Project: project, Interactive: true}, ui)
	if err != nil {
		t.Fatal(err)
	}
	rt.renderSystem()
	if !strings.Contains(rt.system, "## Project instructions (for the agent's work; pass them on only when relevant)\nAlways run make check before a commit.") {
		t.Fatalf("the project instructions are not framed:\n%s", rt.system[max(0, len(rt.system)-600):])
	}
	found := false
	for _, l := range ui.logged() {
		found = found || (strings.Contains(l, "NARRATOR.md") && strings.Contains(l, "no longer read"))
	}
	if !found {
		t.Fatalf("the ignored override was not reported: %v", ui.logged())
	}
}

// The dossier pass may read back as much of the record as the model's
// window has room for, within fixed bounds.
func TestDossierLookupBudgetFollowsTheModelsWindow(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	budget := func(window, prompt int, fresh bool) int {
		var n int
		s.rt.sync(func() {
			s.rt.cfg.Model.ContextTokens, s.rt.cfg.Model.MaxTokens = window, 32768
			n = s.rt.dossierBudget(prompt, fresh)
		})
		return n
	}
	if got := budget(64_000, 60_000, false); got != dossierReadMin {
		t.Fatalf("a nearly full small window gives %d, want the floor %d", got, dossierReadMin)
	}
	mid := budget(200_000, 64_000, false)
	if mid <= dossierReadMin || mid >= dossierReadMax {
		t.Fatalf("a 200k window at 64k gives %d", mid)
	}
	if got := budget(1_000_000, 64_000, false); got != dossierReadMax {
		t.Fatalf("a 1M window gives %d, want the ceiling %d", got, dossierReadMax)
	}
	if got := budget(1_000_000, 64_000, true); got != dossierFreshMax {
		t.Fatalf("a writer with no conversation gets %d, want %d", got, dossierFreshMax)
	}
	if dossierMaxCalls < 30 {
		t.Fatalf("the pass is limited to %d calls", dossierMaxCalls)
	}
}
