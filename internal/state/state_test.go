package state

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

type builder struct {
	seq    int64
	events []event.Event
	t      time.Time
}

func (b *builder) add(ev event.Event) event.Event {
	b.seq++
	ev.Seq = b.seq
	b.t = b.t.Add(time.Second)
	ev.Time = b.t
	b.events = append(b.events, ev)
	return ev
}

func call(id, name, args string) event.ToolCall {
	return event.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)}
}

func newBuilder() *builder {
	b := &builder{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	b.add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "s1", Cwd: "/p"}))
	b.add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1.jsonl", Reason: "new"}))
	return b
}

// startClaude adds a running Claude relay to a log.
func (b *builder) startClaude() {
	b.add(event.New(event.RelayStart, event.ActorHarness, event.RelayStartData{Name: "claude", Profile: "claude", Agent: "claude", Label: "Claude Code", Dir: "/p", Host: "tmux", Target: "eagent-1"}))
}

func flat(msgs []llm.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Role + "\x00" + m.Text)
		for _, tc := range m.ToolCalls {
			sb.WriteString("\x02" + tc.ID + tc.Name + string(tc.Args))
		}
		for _, r := range m.Results {
			sb.WriteString("\x03" + r.CallID + r.Output)
		}
		sb.WriteString("\x01")
	}
	return sb.String()
}

func texts(msgs []llm.Message) string {
	var parts []string
	for _, m := range msgs {
		parts = append(parts, m.Role+": "+m.Text)
	}
	return strings.Join(parts, "\n")
}

func TestViewPlacesResultsAndWhatArrivedMidCall(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "how is it going?", Held: "it is about the session"}))
	seen := b.seq
	// While the model was answering, the agent said something.
	b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "claude", Text: "Running the tests now."}))
	b.add(event.New(event.Steer, event.ActorEagent, event.SteerData{Text: "[woken: a message from the user]"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "relay_screen", `{}`)}, SeenSeq: seen}))
	b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: "c1", Name: "relay_screen", Output: "$ go test ./..."}))

	msgs := Replay(b.events).View(nil)
	var roles []string
	for _, m := range msgs {
		roles = append(roles, m.Role)
	}
	// relay.start, the user's message, the steer, the reply, its result,
	// then the remark that arrived during the call.
	if got := strings.Join(roles, ","); got != "user,user,user,assistant,tool,user" {
		t.Fatalf("roles = %s\n%s", got, texts(msgs))
	}
	if !strings.Contains(msgs[1].Text, "how is it going?") || !strings.Contains(msgs[1].Text, "[Not passed to the agent: it is about the session.]") {
		t.Fatalf("the held message does not say it was held: %q", msgs[1].Text)
	}
	if msgs[2].Text != "[woken: a message from the user]" {
		t.Fatalf("the steer must sit right before the reply it led to, got %q", msgs[2].Text)
	}
	if msgs[4].Results[0].Output != "$ go test ./..." {
		t.Fatalf("tool result misplaced: %+v", msgs[4])
	}
	if !strings.Contains(msgs[5].Text, "<Claude> Running the tests now.") {
		t.Fatalf("the mid-call remark must follow the call's results, got %q", msgs[5].Text)
	}
}

// The prompt of one look must be a strict prefix of the prompt of the next,
// whether the look ended in silence or in an action. That is what keeps the
// provider's cache of the conversation valid.
func TestEveryPromptExtendsTheOneBefore(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "add a dark mode"}))
	b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Seq: b.seq, Mode: "queue", Text: "add a dark mode", Auto: true}))
	b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "claude", Text: "I'll start with the theme tokens."}))

	// Look 1: a progress tick that ends in a hold. Nothing is recorded.
	tail1 := "[12:00:30 progress look] Claude has been working for 25s."
	prompt1 := WithTail(Replay(b.events).View(nil), tail1)

	// More happens.
	b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "claude", Text: "Tokens are in; wiring the toggle."}))

	// Look 2: the model speaks. The tail is recorded as a steer, then the
	// reply, while a remark lands mid-call.
	tail2 := "[12:01:15 progress look] Claude has been working for 70s; the user has heard nothing for 70s."
	seen := b.seq
	prompt2 := WithTail(Replay(b.events).View(nil), tail2)
	b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "claude", Text: "Toggle works in the header."}))
	b.add(event.New(event.TurnStart, event.ActorEagent, event.TurnData{}))
	b.add(event.New(event.Steer, event.ActorEagent, event.SteerData{Text: tail2}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "send_message", `{"text":"Claude has the theme tokens in and is wiring the toggle."}`)}, SeenSeq: seen}))
	b.add(event.New(event.Message, event.ActorEagent, event.MessageData{Text: "Claude has the theme tokens in and is wiring the toggle."}))
	b.add(event.New(event.ToolResult, event.ActorEagent, event.ToolResultData{CallID: "c1", Name: "send_message", Output: "sent"}))
	b.add(event.New(event.TurnEnd, event.ActorEagent, event.TurnData{}))

	// Look 3.
	prompt3 := WithTail(Replay(b.events).View(nil), "[12:02:00 progress look]")

	p1, p2, p3 := flat(prompt1[:len(prompt1)-1]), flat(prompt2[:len(prompt2)-1]), flat(prompt3[:len(prompt3)-1])
	if !strings.HasPrefix(p2, p1) {
		t.Fatalf("look 2 does not extend look 1 (a silent look left a trace, or the record was rewritten)\n1: %q\n2: %q", p1, p2)
	}
	// Look 3 must extend all of look 2, tail included: the tail was recorded.
	if full2 := flat(prompt2); !strings.HasPrefix(p3, full2) {
		t.Fatalf("look 3 does not extend look 2 including its tail\n2: %q\n3: %q", full2, p3)
	}
	// And the remark that landed mid-call shows after the reply's result.
	last := prompt3[len(prompt3)-2]
	if !strings.Contains(last.Text, "Toggle works in the header.") {
		t.Fatalf("the mid-call remark should be the newest line, got %q", last.Text)
	}
}

func TestDanglingCallGetsSyntheticError(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "go"}))
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "relay_screen", `{}`)}, SeenSeq: 3}))
	st := Replay(b.events)
	if d := st.DanglingCalls(event.ActorEagent); len(d) != 1 || d[0].ID != "c1" {
		t.Fatalf("dangling calls = %+v", d)
	}
	msgs := st.View(nil)
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || !last.Results[0].IsError || !strings.Contains(last.Results[0].Output, "interrupted") {
		t.Fatalf("a call without a result must render an error result, got %+v", last)
	}
}

func TestBriefingOpensTheConversationAndNothingAfterItIsLost(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "old request"}))
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "old report"}))
	covers := b.seq
	// The briefing is being written. Meanwhile the user writes again and
	// the agent says something; both land in the old file.
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "one more thing"}))
	b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "claude", Text: "On it."}))
	b.add(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "rollover", NextFile: "2.jsonl"}))
	b.add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "2.jsonl", Index: 1, Reason: "rollover"}))
	b.add(event.New(event.Dossier, event.ActorHarness, event.DossierData{Text: "WHAT THE USER WANTS\nthe old request", CoversSeq: covers, By: "eagent", Usage: &event.Usage{Input: 50_000, Output: 400, Cached: 49_000}, ElapsedMS: 3000}))
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "new report"}))

	st := Replay(b.events)
	if len(st.Subsessions) != 2 || st.Current().CoversSeq != covers || !strings.Contains(st.Current().Dossier, "the old request") {
		t.Fatalf("subsessions = %+v", st.Subsessions)
	}
	msgs := st.View(nil)
	all := texts(msgs)
	if !strings.HasPrefix(msgs[0].Text, "[Briefing.") || !strings.Contains(msgs[0].Text, "WHAT THE USER WANTS") {
		t.Fatalf("the briefing must open the conversation, got %q", msgs[0].Text)
	}
	for _, gone := range []string{"old request\n", "old report"} {
		if strings.Contains(strings.TrimPrefix(all, "user: "+msgs[0].Text), gone) {
			t.Fatalf("%q is covered by the briefing and must not be rendered again:\n%s", gone, all)
		}
	}
	for _, kept := range []string{"one more thing", "On it.", "new report"} {
		if !strings.Contains(all, kept) {
			t.Fatalf("%q arrived after the briefing was taken and must be in the conversation:\n%s", kept, all)
		}
	}
	if strings.Index(all, "one more thing") > strings.Index(all, "new report") {
		t.Fatalf("events are out of order:\n%s", all)
	}
	// The briefing's model call is eagent's, and the prompt size starts over.
	if st.Totals[event.ActorEagent].Input != 50_000 || st.Calls[event.ActorEagent] != 1 {
		t.Fatalf("briefing usage not counted: %+v", st.Totals)
	}
	if st.ContextTokens() != 0 {
		t.Fatalf("context should start over in a new subsession, got %d", st.ContextTokens())
	}
}

func TestIDCountersContinueAfterReplay(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q7", Text: "?"}))
	b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r4", From: "eagent", Text: "x"}))
	st := Replay(b.events)
	if id := st.NextQuestionID(); id != "q8" {
		t.Fatalf("next question id = %s", id)
	}
	if id := st.NextRelaySendID(); id != "r5" {
		t.Fatalf("next send id = %s", id)
	}
}

func TestImagesOfSkipsUnsupportedTypes(t *testing.T) {
	ev := event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "look", Attachments: []event.Attachment{
		{Name: "a.png", Path: "/x/a.png", ContentType: "image/png"},
		{Name: "b.heic", Path: "/x/b.heic", ContentType: "image/heic"},
	}})
	if imgs := imagesOf(ev); len(imgs) != 1 || imgs[0].Path != "/x/a.png" {
		t.Fatalf("images = %+v", imgs)
	}
}

func TestSessionFolderComesFromTheStartNotFromOldShellCds(t *testing.T) {
	b := newBuilder()
	st := Replay(b.events)
	if st.WorkDir() != "/p" || st.Dir != "" {
		t.Fatalf("a session works in the project root by default, got %q / %q", st.WorkDir(), st.Dir)
	}
	b.add(event.New(event.CwdChange, event.ActorHarness, event.CwdChangeData{Path: "/p/app", Previous: "/p"}))
	// A log from when workers had shells: a task's cd is not the session's.
	b.add(event.New(event.CwdChange, event.LegacyTask, event.CwdChangeData{Path: "/tmp/elsewhere"}).WithTask("t1"))
	st = Replay(b.events)
	if st.WorkDir() != "/p/app" || st.Dir != "/p/app" {
		t.Fatalf("session folder = %q / %q", st.WorkDir(), st.Dir)
	}
}

func TestSilentLooksCountTowardTotalsAndContext(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.Assistant, event.ActorEagent, event.AssistantData{Text: "", Usage: event.Usage{Input: 1000, Output: 20, Cached: 900}, ElapsedMS: 800}))
	b.add(event.New(event.Checks, event.ActorEagent, event.ChecksData{Count: 3, Usage: event.Usage{Input: 3300, Output: 30, Cached: 3000}, PromptTokens: 1150}))
	st := Replay(b.events)
	if st.Calls[event.ActorEagent] != 4 {
		t.Fatalf("calls = %d, want 4", st.Calls[event.ActorEagent])
	}
	if tot := st.Totals[event.ActorEagent]; tot.Input != 4300 || tot.Cached != 3900 || tot.Output != 50 {
		t.Fatalf("totals = %+v", tot)
	}
	if st.ContextTokens() != 1150 {
		t.Fatalf("context = %d, want the size of the last silent prompt", st.ContextTokens())
	}
	if len(st.Latencies[event.ActorEagent]) != 1 {
		t.Fatalf("silent looks have no single latency to record: %v", st.Latencies)
	}
	if line := st.UsageLine(); !strings.Contains(line, "eagent 4 calls") || !strings.Contains(line, "90% cached") {
		t.Fatalf("usage line = %q", line)
	}
}

func TestDismissClosesQuestionAndStandsUntilSomethingMoves(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "Delete the old reader?"}))
	b.add(event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q1", Text: "Delete the old reader?", Options: []string{"Yes", "No"}}))
	st := Replay(b.events)
	if st.Question == nil || st.DismissalStands() {
		t.Fatalf("question should be pending")
	}
	b.add(event.New(event.UserDismiss, event.ActorUser, event.UserDismissData{QuestionID: "q1"}))
	st = Replay(b.events)
	if st.Question != nil || !st.DismissalStands() {
		t.Fatalf("a dismissed question is closed and must not be asked again")
	}
	if all := texts(st.View(nil)); !strings.Contains(all, "dismissed question q1 without answering") {
		t.Fatalf("eagent is not told about the dismissal:\n%s", all)
	}
	// The agent finishing another turn is new ground.
	moved := append([]event.Event{}, b.events...)
	b2 := &builder{seq: b.seq, events: moved, t: b.t}
	b2.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "Also: tests pass."}))
	if Replay(b2.events).DismissalStands() {
		t.Fatalf("a new turn from the agent ends the dismissal's hold")
	}
	// So is the user writing.
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "keep it"}))
	if Replay(b.events).DismissalStands() {
		t.Fatalf("a message from the user ends the dismissal's hold")
	}
}

func TestAwayAndBackAreSaidOnceEach(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", RemoteMode: false}))
	b.add(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", RemoteMode: true}))
	b.add(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", RemoteMode: true}))
	b.add(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t", RemoteMode: false}))
	all := texts(Replay(b.events).View(nil))
	if strings.Count(all, "away from the terminal") != 1 || strings.Count(all, "back at the terminal") != 1 {
		t.Fatalf("want one line for leaving and one for returning:\n%s", all)
	}
}

func TestAgentLinesAndBounds(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "fix the importer"}))
	b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Seq: b.seq, Mode: "interrupt", Text: "fix the importer", Auto: true}))
	b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r2", From: "eagent", Mode: "queue", Text: "the fixture is at testdata/big.csv"}))
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "working"}))
	b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "claude", Text: strings.Repeat("r", 5000)}))
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "blocked", Detail: "Do you trust this folder? 1. Yes 2. No"}))
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: strings.Repeat("R", 20_000), DurationMS: 400_000, Tools: 23, Images: []string{"/p/shot.png"}}))
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: "r2", OK: false, Detail: "not seen in its transcript after 60s"}))
	st := Replay(b.events)
	msgs := st.View(&ViewOptions{RemarkChars: 200, ReportChars: 1000})
	all := texts(msgs)
	for _, want := range []string{
		"Started Claude Code in /p.]",
		"Passed to Claude as written, interrupting what it was doing.]",
		"Claude needs attention: Do you trust this folder?",
		"Claude finished a turn after 6m40s, 23 tool calls. Its report:]",
		"[Pictures it made or looked at: /p/shot.png]",
		"A message to Claude (r2) could not be confirmed: not seen in its transcript after 60s]",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("conversation lacks %q:\n%s", want, all)
		}
	}
	// A send eagent made with its own tool call is not repeated as a line,
	// and plain working/idle changes say nothing.
	if strings.Contains(all, "testdata/big.csv") || strings.Contains(all, "working]") {
		t.Errorf("redundant lines in the conversation:\n%s", all)
	}
	for _, m := range msgs {
		if strings.Contains(m.Text, "<Claude> rrr") && len(m.Text) > 400 {
			t.Errorf("a remark is not bounded: %d chars", len(m.Text))
		}
		if strings.Contains(m.Text, "<Claude> RRR") && len(m.Text) > 1300 {
			t.Errorf("a report is not bounded: %d chars", len(m.Text))
		}
	}
}

func TestClockTimesUseTheSessionsZoneEverywhere(t *testing.T) {
	b := &builder{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	b.add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "s1", Cwd: "/p", TZ: "Asia/Tokyo"}))
	b.add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1.jsonl", Reason: "new"}))
	b.startClaude()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "hello"}))
	all := texts(Replay(b.events).View(nil))
	// 12:00 UTC is 21:00 in Tokyo, whatever zone this test runs in.
	if !strings.Contains(all, "[21:00:03 Started Claude Code") || !strings.Contains(all, "[2026-09-06 21:00:04] <user> hello") {
		t.Fatalf("times are not in the session's zone:\n%s", all)
	}
}

func TestWaiting(t *testing.T) {
	b := newBuilder()
	if !Replay(b.events).Waiting() {
		t.Fatalf("a session with no agent waits on the user")
	}
	b.startClaude()
	if Replay(b.events).Waiting() {
		t.Fatalf("a starting agent is not waiting")
	}
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "idle"}))
	if !Replay(b.events).Waiting() {
		t.Fatalf("an idle agent waits on the user")
	}
	b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Text: "go", Auto: true}))
	if Replay(b.events).Waiting() {
		t.Fatalf("a message on its way to the agent is not waiting")
	}
	b.add(event.New(event.RelayDelivered, event.ActorHarness, event.RelayDeliveredData{Name: "claude", ID: "r1", OK: true}))
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "working"}))
	if Replay(b.events).Waiting() {
		t.Fatalf("a working agent is not waiting")
	}
	b.add(event.New(event.RelayTurn, event.ActorHarness, event.RelayTurnData{Name: "claude", Text: "done"}))
	if !Replay(b.events).Waiting() {
		t.Fatalf("after its turn the agent waits on the user")
	}
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude"}))
	if !Replay(b.events).Waiting() {
		t.Fatalf("a stopped agent leaves the session waiting")
	}
}

// An agent that is gone confirms nothing more: a send the record still
// lists as unconfirmed for it must not hold the session, whether or not
// anything wrote its outcome.
func TestASendToAnAgentThatIsGoneIsNotAwaited(t *testing.T) {
	b := newBuilder()
	b.startClaude()
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "idle"}))
	b.add(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "claude", ID: "r1", From: "user", Text: "go", Auto: true}))
	st := Replay(b.events)
	if st.Waiting() || st.Relays["claude"].Awaited() != 1 {
		t.Fatalf("a send to a running agent is awaited")
	}
	b.add(event.New(event.RelayState, event.ActorHarness, event.RelayStateData{Name: "claude", State: "exited"}))
	st = Replay(b.events)
	if rel := st.Relays["claude"]; !st.Waiting() || rel.Awaited() != 0 || len(rel.Undelivered) != 1 {
		t.Fatalf("an exited agent: waiting %v, awaited %d, listed %d", st.Waiting(), rel.Awaited(), len(rel.Undelivered))
	}
	b.add(event.New(event.RelayEnd, event.ActorHarness, event.RelayEndData{Name: "claude"}))
	st = Replay(b.events)
	if rel := st.Relays["claude"]; !st.Waiting() || rel.Awaited() != 0 {
		t.Fatalf("a stopped agent: waiting %v, awaited %d", st.Waiting(), rel.Awaited())
	}
}

func TestSummaryCallsAreCountedUnderWhoMadeThem(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.ThreadTitle, event.ActorSummarizer, event.ThreadTitleData{Title: "Importer rewrite", Description: "Rewriting the importer.", By: "summary", SeenSeq: 2, Usage: &event.Usage{Input: 900, Output: 40}, ElapsedMS: 700}))
	// An inline update is part of one of eagent's own calls: no usage.
	b.add(event.New(event.ThreadTitle, event.ActorEagent, event.ThreadTitleData{Description: "Rewriting the importer. Tests pass.", By: "summary", SeenSeq: 3}))
	st := Replay(b.events)
	if st.Calls[event.ActorSummarizer] != 1 || st.Totals[event.ActorSummarizer].Input != 900 || st.Calls[event.ActorEagent] != 0 {
		t.Fatalf("calls = %+v totals = %+v", st.Calls, st.Totals)
	}
	if st.Title != "Importer rewrite" || st.Description != "Rewriting the importer. Tests pass." || st.SummarySeq != 3 || st.TitleRevisions != 1 {
		t.Fatalf("title state = %q / %q / %d / %d", st.Title, st.Description, st.SummarySeq, st.TitleRevisions)
	}
	// A title the user asked for is pinned; later summaries keep that.
	b.add(event.New(event.ThreadTitle, event.ActorEagent, event.ThreadTitleData{Title: "Big CSV import", Pin: true}))
	b.add(event.New(event.ThreadTitle, event.ActorEagent, event.ThreadTitleData{Title: "Big CSV import", Description: "Later.", By: "summary", SeenSeq: 5}))
	st = Replay(b.events)
	if !st.TitlePinned || st.Title != "Big CSV import" {
		t.Fatalf("pinned title lost: %q pinned=%v", st.Title, st.TitlePinned)
	}
}

// A log written by the three-actor design still folds: its usage shows under
// the old names, its events do not disturb the picture, and it is recognised
// for what it is.
func TestOldLogsStillFold(t *testing.T) {
	b := &builder{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	b.add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "old", Cwd: "/p", Models: map[string]string{"orchestrator": "m-o", "task": "m-t", "narrator": "m-n"}}))
	b.add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1.jsonl", Reason: "new"}))
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "build a thing"}))
	b.add(event.New(event.Assistant, event.LegacyOrchestrator, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "delegate", `{"title":"x"}`)}, Usage: event.Usage{Input: 2000, Output: 100}, ElapsedMS: 900}))
	b.add(event.New(event.LegacyTaskCreate, event.LegacyOrchestrator, map[string]any{"id": "t1", "title": "x", "description": "do x"}))
	b.add(event.New(event.Assistant, event.LegacyTask, event.AssistantData{Text: "working", Usage: event.Usage{Input: 5000, Output: 300}, ElapsedMS: 1200}).WithTask("t1"))
	b.add(event.New(event.LegacyTaskEnd, event.LegacyTask, map[string]any{"id": "t1", "status": "completed", "summary": "did x"}).WithTask("t1"))
	b.add(event.New(event.Checks, event.LegacyNarrator, event.ChecksData{Count: 2, Usage: event.Usage{Input: 800, Output: 10}}))
	b.add(event.New(event.ThreadTitle, event.ActorHarness, event.ThreadTitleData{Title: "A thing", By: "summary", Usage: &event.Usage{Input: 300, Output: 20}, ElapsedMS: 500}))
	b.add(event.New(event.Message, event.LegacyNarrator, event.MessageData{Text: "It built the thing."}))
	b.add(event.New(event.LegacyYield, event.LegacyOrchestrator, event.LegacyYieldData{Done: true, Reason: "built"}))
	b.add(event.New(event.SessionEnd, event.ActorHarness, event.SessionEndData{Reason: "done"}))
	st := Replay(b.events)
	if !st.Legacy() {
		t.Fatalf("an old log should be recognised")
	}
	if got := strings.Join(st.UsageActors(), ","); got != "orchestrator,task,narrator" {
		t.Fatalf("usage actors = %s", got)
	}
	if st.Calls[event.LegacyNarrator] != 3 || st.Totals[event.LegacyNarrator].Input != 1100 {
		t.Fatalf("narrator usage = %d calls, %+v", st.Calls[event.LegacyNarrator], st.Totals[event.LegacyNarrator])
	}
	if st.ContextTokens() != 2000 || !st.Ended || st.EndReason != "done" || st.Title != "A thing" {
		t.Fatalf("old session picture is off: ctx=%d ended=%v reason=%q title=%q", st.ContextTokens(), st.Ended, st.EndReason, st.Title)
	}
	if Replay(newBuilder().events).Legacy() {
		t.Fatalf("a new log is not an old one")
	}
}

// Who names a relay's agent by the short name relay.start recorded, and,
// for logs written before that, by the agent's name capitalised.
func TestRelayWho(t *testing.T) {
	for _, tc := range []struct {
		r    Relay
		want string
	}{
		{Relay{Name: "c", Agent: "claude", AgentLabel: "Claude"}, "Claude"},
		{Relay{Name: "c", Agent: "claude"}, "Claude"},
		{Relay{Name: "x", Agent: "antigravity"}, "Antigravity"},
		{Relay{Name: "x", Agent: "pi"}, "Pi"},
		{Relay{Name: "x"}, "x"},
	} {
		if got := tc.r.Who(); got != tc.want {
			t.Fatalf("%+v: Who = %q, want %q", tc.r, got, tc.want)
		}
	}
}
