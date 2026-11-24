package state

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
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

func TestOrchestratorViewPlacesResultsAndDeferredNotifications(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "build it"}))                                                          // 3
	b.add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "delegate", `{}`)}, SeenSeq: 3})) // 4
	b.add(event.New(event.TaskCreate, event.ActorOrchestrator, event.TaskCreateData{ID: "t1", Title: "x", Description: "do x", Kind: "work"}))             // 5
	b.add(event.New(event.ToolResult, event.ActorOrchestrator, event.ToolResultData{CallID: "c1", Name: "delegate", Output: "Task t1 started"}))           // 6
	// A task result lands while the next model call (seen through 6) is in flight.
	b.add(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: "t1", Status: "completed", Summary: "done x"}).WithTask("t1"))                          // 7
	b.add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{ToolCalls: []event.ToolCall{call("c2", "bash", `{"command":"ls"}`)}, SeenSeq: 6})) // 8
	b.add(event.New(event.ToolResult, event.ActorOrchestrator, event.ToolResultData{CallID: "c2", Name: "bash", Output: "a.txt"}))                                   // 9
	// Task worker internals must not appear.
	b.add(event.New(event.Assistant, event.ActorTask, event.AssistantData{Text: "worker thinking"}).WithTask("t1")) // 10

	st := Replay(b.events)
	msgs := st.OrchestratorView()
	var roles []string
	for _, m := range msgs {
		roles = append(roles, m.Role)
	}
	want := []string{"user", "assistant", "tool", "assistant", "tool", "user"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	if !strings.Contains(msgs[5].Text, "Task t1 completed") {
		t.Fatalf("deferred notification missing: %q", msgs[5].Text)
	}
	if msgs[2].Results[0].Output != "Task t1 started" {
		t.Fatalf("tool result misplaced: %+v", msgs[2].Results)
	}
	if st.Tasks["t1"].Status != "completed" || st.Idle() {
		t.Fatalf("task=%s idle=%v", st.Tasks["t1"].Status, st.Idle())
	}
}

func TestDanglingCallGetsSyntheticError(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "go"}))
	b.add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "bash", `{}`)}, SeenSeq: 3}))
	st := Replay(b.events)
	if d := st.DanglingCalls(event.ActorOrchestrator, ""); len(d) != 1 || d[0].ID != "c1" {
		t.Fatalf("dangling = %+v", d)
	}
	msgs := st.OrchestratorView()
	if msgs[2].Role != "tool" || !msgs[2].Results[0].IsError {
		t.Fatalf("expected synthetic error result, got %+v", msgs[2])
	}
}

func TestYieldAndWakeSemantics(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "go"}))                                                             // 3
	b.add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "yield", `{}`)}, SeenSeq: 3})) // 4
	b.add(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: false, Reason: "waiting"}))                                             // 5
	st := Replay(b.events)
	if !st.Idle() {
		t.Fatal("should be idle after yield")
	}
	b.add(event.New(event.ScheduleFire, event.ActorHarness, event.ScheduleFireData{ID: "s1", Note: "tick"})) // 6
	st = Replay(b.events)
	if st.Idle() {
		t.Fatal("schedule fire must wake")
	}
	b.add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{ToolCalls: []event.ToolCall{call("c2", "yield", `{}`)}, SeenSeq: 6})) // 7
	b.add(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: true, Reason: "finished"}))
	b.add(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: "d1", Status: "completed"}).WithTask("d1"))
	st = Replay(b.events)
	// Unknown task id (not registered) must not wake; only known work tasks do.
	if !st.Idle() {
		t.Fatal("task end for an unknown task should not wake")
	}
}

func TestNarratorViewBatchesObservations(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "go"}))                                                                                        // 3
	b.add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{Text: "plan", ToolCalls: []event.ToolCall{call("c1", "bash", `{"command":"ls"}`)}, SeenSeq: 3})) // 4
	b.add(event.New(event.ToolResult, event.ActorOrchestrator, event.ToolResultData{CallID: "c1", Name: "bash", Output: strings.Repeat("x", 5000)}))                               // 5
	b.add(event.New(event.Note, event.ActorOrchestrator, event.NoteData{Text: "milestone"}))                                                                                       // 6
	// Narrator turn saw through 6; a task-worker event and a new note arrive during it.
	b.add(event.New(event.Assistant, event.ActorTask, event.AssistantData{Text: "internal"}).WithTask("t1"))                                                          // 7
	b.add(event.New(event.Note, event.ActorOrchestrator, event.NoteData{Text: "second"}))                                                                             // 8
	b.add(event.New(event.Assistant, event.ActorNarrator, event.AssistantData{ToolCalls: []event.ToolCall{call("n1", "send_message", `{"text":"hi"}`)}, SeenSeq: 6})) // 9
	b.add(event.New(event.NarratorMessage, event.ActorNarrator, event.NarratorMessageData{Text: "hi"}))                                                               // 10
	b.add(event.New(event.ToolResult, event.ActorNarrator, event.ToolResultData{CallID: "n1", Name: "send_message", Output: "delivered"}))                            // 11

	st := Replay(b.events)
	msgs := st.NarratorView(nil)
	if len(msgs) != 4 {
		t.Fatalf("expected user, assistant, tool, user; got %d: %+v", len(msgs), msgs)
	}
	first := msgs[0].Text
	if !strings.Contains(first, "USER: go") || !strings.Contains(first, "NOTE FROM ORCHESTRATOR") || !strings.Contains(first, "milestone") {
		t.Fatalf("first batch = %q", first)
	}
	if strings.Contains(first, "second") {
		t.Fatal("observation after seenSeq leaked into the earlier batch")
	}
	if strings.Contains(first, "internal") {
		t.Fatal("task worker internals leaked to the narrator")
	}
	if len(first) > 4000 {
		t.Fatalf("tool output not clipped for the narrator: %d chars", len(first))
	}
	if !strings.Contains(msgs[3].Text, "second") {
		t.Fatalf("held observation missing from the next batch: %q", msgs[3].Text)
	}
	// Prefix stability: adding a new event must not change earlier messages.
	b.add(event.New(event.Note, event.ActorOrchestrator, event.NoteData{Text: "third"}))
	msgs2 := Replay(b.events).NarratorView(nil)
	for i := 0; i < 3; i++ {
		if msgs2[i].Text != msgs[i].Text {
			t.Fatalf("message %d changed after append", i)
		}
	}
}

func TestSubsessionAndDossier(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "go"}))
	b.add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{Text: "old", Usage: event.Usage{Input: 190000}, SeenSeq: 3}))
	b.add(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "full", NextFile: "2.jsonl"}))
	b.add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "2.jsonl", Index: 1, Reason: "rollover"}))
	b.add(event.New(event.TaskCreate, event.ActorHarness, event.TaskCreateData{ID: "t9", Kind: "dossier", Title: "Dossier", Description: "..."}))
	// A work task finishes before the dossier lands.
	b.add(event.New(event.TaskCreate, event.ActorOrchestrator, event.TaskCreateData{ID: "t2", Kind: "work", Title: "w", Description: "..."}))
	b.add(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: "t2", Status: "completed", Summary: "w done"}).WithTask("t2"))
	b.add(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: "t9", Status: "completed", Summary: "the dossier"}).WithTask("t9"))
	b.add(event.New(event.Dossier, event.ActorHarness, event.DossierData{TaskID: "t9", Text: "the dossier"}))

	st := Replay(b.events)
	if len(st.Subsessions) != 2 || st.Current().File != "2.jsonl" || st.Current().Dossier != "the dossier" {
		t.Fatalf("subsessions = %+v", st.Subsessions)
	}
	if st.ContextTokens(event.ActorOrchestrator) != 0 {
		t.Fatal("context usage should reset at a new subsession")
	}
	msgs := st.OrchestratorView()
	if len(msgs) < 2 || !strings.Contains(msgs[0].Text, "Dossier") || !strings.Contains(msgs[0].Text, "the dossier") {
		t.Fatalf("dossier must open the new subsession; got %+v", msgs)
	}
	joined := ""
	for _, m := range msgs {
		joined += m.Text + "\n"
	}
	if n := strings.Count(joined, "the dossier"); n != 1 {
		t.Fatalf("the dossier must reach the orchestrator once, not %d times: %s", n, joined)
	}
	if strings.Contains(joined, "Task t9") {
		t.Fatalf("the dossier task's end must not be reported as a task: %s", joined)
	}
	if !strings.Contains(msgs[1].Text, "Task t2 completed") {
		t.Fatalf("work task result missing: %+v", msgs[1])
	}
	for _, m := range msgs {
		if strings.Contains(m.Text, "old") {
			t.Fatal("previous subsession leaked into the new view")
		}
	}
	if st.Idle() {
		t.Fatal("dossier arrival must wake the orchestrator")
	}
}

func TestIDCountersContinueAfterReplay(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.TaskCreate, event.ActorOrchestrator, event.TaskCreateData{ID: "t3", Kind: "work"}))
	b.add(event.New(event.ProcStart, event.ActorOrchestrator, event.ProcStartData{Handle: "p7", Command: "ls"}))
	b.add(event.New(event.ScheduleCreate, event.ActorOrchestrator, event.ScheduleCreateData{ID: "s2", Kind: "loop", Spec: "5m"}))
	st := Replay(b.events)
	if st.NextTaskID() != "t4" || st.NextProcHandle() != "p8" || st.NextScheduleID() != "s3" {
		t.Fatal("counters did not continue")
	}
}

func TestStaleYieldDoesNotIdle(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "go"}))                               // 3
	b.add(event.New(event.TaskCreate, event.ActorOrchestrator, event.TaskCreateData{ID: "t1", Kind: "work", Title: "w"})) // 4
	// The yielding call saw through 4; t1 finishes while it is in flight.
	b.add(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: "t1", Status: "completed", Summary: "done"}).WithTask("t1"))               // 5
	b.add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "yield", `{}`)}, SeenSeq: 4})) // 6
	b.add(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: false, Reason: "waiting for t1"}))                                      // 7
	st := Replay(b.events)
	if st.Idle() {
		t.Fatal("a yield that predates an unseen task result must not make the session idle")
	}
	// Once a later call has seen everything, a yield counts.
	b.add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{ToolCalls: []event.ToolCall{call("c2", "yield", `{}`)}, SeenSeq: 7}))
	b.add(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: true, Reason: "finished"}))
	if st = Replay(b.events); !st.Idle() || !st.LastYield.Done {
		t.Fatal("a yield after seeing everything must idle the session")
	}
}

func TestForcedYieldAlwaysIdles(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "go"}))
	// No assistant event ever succeeded (every model call failed), then the
	// harness forces a yield: the session must be idle, not loop.
	b.add(event.New(event.HarnessMessage, event.ActorOrchestrator, event.HarnessMessageData{Text: "recover"}))
	b.add(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: false, Forced: true, Reason: "failed"}))
	if st := Replay(b.events); !st.Idle() {
		t.Fatal("forced yield must idle the session")
	}
}

// An answer to a question asked in an earlier context restates the question
// in the orchestrator's view, so a terse option stays readable.
func TestAnswerAcrossRolloverCarriesTheQuestion(t *testing.T) {
	st := New()
	seq := int64(0)
	add := func(ev event.Event) {
		seq++
		ev.Seq = seq
		st.Apply(ev)
	}
	add(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{}))
	add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "1.jsonl", Index: 0, Reason: "start"}))
	add(event.New(event.NarratorQuestion, event.ActorNarrator, event.NarratorQuestionData{ID: "q1", Text: "Which database?", Options: []string{"SQLite", "Postgres"}}))
	add(event.New(event.SubsessionEnd, event.ActorHarness, event.SubsessionEndData{Reason: "context full", NextFile: "2.jsonl"}))
	add(event.New(event.SubsessionStart, event.ActorHarness, event.SubsessionStartData{File: "2.jsonl", Index: 1, Reason: "rollover"}))
	add(event.New(event.TaskCreate, event.ActorHarness, event.TaskCreateData{ID: "t9", Kind: "dossier", Title: "Dossier"}))
	add(event.New(event.TaskEnd, event.ActorHarness, event.TaskEndData{ID: "t9", Status: "completed", Summary: "the dossier"}).WithTask("t9"))
	add(event.New(event.Dossier, event.ActorHarness, event.DossierData{TaskID: "t9", Text: "the dossier"}))
	add(event.New(event.UserAnswer, event.ActorUser, event.UserAnswerData{QuestionID: "q1", Text: "SQLite"}))
	joined := ""
	for _, m := range st.OrchestratorView() {
		joined += m.Text + "\n"
	}
	if !strings.Contains(joined, "Which database?") || !strings.Contains(joined, "SQLite | Postgres") || !strings.Contains(joined, "SQLite") {
		t.Fatalf("the answer lost its question across the rollover: %s", joined)
	}
	if st.QuestionText("nope") != "" {
		t.Fatal("unknown question ids have no text")
	}
}

// Only picture formats every vision model accepts are inlined; a phone's
// HEIC stays a file the note mentions.
func TestImagesOfSkipsUnsupportedTypes(t *testing.T) {
	ev := event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "look", Attachments: []event.Attachment{
		{Name: "a.heic", Path: "/x/a.heic", ContentType: "image/heic", Kind: "image"},
		{Name: "b.png", Path: "/x/b.png", ContentType: "image/png", Kind: "image"},
	}})
	imgs := imagesOf(ev)
	if len(imgs) != 1 || imgs[0].Path != "/x/b.png" {
		t.Fatalf("images = %+v", imgs)
	}
}

func TestWorkingDirectoryFollowsCdEventsPerActor(t *testing.T) {
	st := New()
	st.Apply(event.New(event.SessionStart, event.ActorHarness, event.SessionStartData{Session: "1", Cwd: "/proj"}))
	if st.WorkDir("") != "/proj" || st.WorkDir("t9") != "/proj" {
		t.Fatalf("fresh session: %q %q", st.WorkDir(""), st.WorkDir("t9"))
	}
	st.Apply(event.New(event.CwdChange, event.ActorOrchestrator, event.CwdChangeData{Path: "/proj/sub", Previous: "/proj"}))
	if st.WorkDir("") != "/proj/sub" {
		t.Fatalf("orchestrator cd not applied: %q", st.WorkDir(""))
	}
	// A worker starts where the orchestrator was, and keeps it when the
	// orchestrator moves on; its own cd is its own.
	st.Apply(event.New(event.TaskCreate, event.ActorOrchestrator, event.TaskCreateData{ID: "t1", Title: "x", Description: "y", Kind: "work"}))
	st.Apply(event.New(event.CwdChange, event.ActorOrchestrator, event.CwdChangeData{Path: "/elsewhere"}))
	if st.WorkDir("t1") != "/proj/sub" || st.WorkDir("") != "/elsewhere" {
		t.Fatalf("task inheritance: task=%q orch=%q", st.WorkDir("t1"), st.WorkDir(""))
	}
	st.Apply(event.New(event.CwdChange, event.ActorTask, event.CwdChangeData{Path: "/proj/sub/deeper"}).WithTask("t1"))
	if st.WorkDir("t1") != "/proj/sub/deeper" || st.WorkDir("") != "/elsewhere" {
		t.Fatalf("task cd leaked: task=%q orch=%q", st.WorkDir("t1"), st.WorkDir(""))
	}
	// Replaying the same events gives the same answer.
	again := Replay(st.Events)
	if again.WorkDir("t1") != "/proj/sub/deeper" || again.WorkDir("") != "/elsewhere" {
		t.Fatal("replay lost the working directories")
	}
	if got := Observe(st.Events[1], 200); !strings.Contains(got, "/proj/sub") {
		t.Fatalf("narrator does not see the move: %q", got)
	}
	if got := Observe(st.Events[len(st.Events)-1], 200); got != "" {
		t.Fatalf("a worker's cd is not the narrator's business: %q", got)
	}
}

func TestAnswerNotesAreMarkedForTheNarrator(t *testing.T) {
	ev := event.New(event.Note, event.ActorOrchestrator, event.NoteData{Text: "Mostly the briefing.", Answer: true})
	if got := Observe(ev, 200); !strings.Contains(got, "ANSWER FROM THE ORCHESTRATOR") || !strings.Contains(got, "Mostly the briefing.") {
		t.Fatalf("answer note rendering: %q", got)
	}
	plain := event.New(event.Note, event.ActorOrchestrator, event.NoteData{Text: "milestone"})
	if got := Observe(plain, 200); strings.Contains(got, "ANSWER") {
		t.Fatalf("plain note rendered as an answer: %q", got)
	}
}

// A progress check shows the narrator a steer that is recorded only if it
// speaks: WithSteer must render exactly what the recorded steer would.
func TestWithSteerMatchesARecordedSteer(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "go"}))
	b.add(event.New(event.Note, event.ActorOrchestrator, event.NoteData{Text: "milestone"}))
	const steer = "[harness] Progress check while work goes on."
	shown := WithSteer(Replay(b.events).NarratorView(nil), steer)
	seen := b.seq
	// A remark lands while the model answers; then the turn is recorded.
	b.add(event.New(event.Note, event.ActorOrchestrator, event.NoteData{Text: "arrived during the call"}))
	b.add(event.New(event.TurnStart, event.ActorNarrator, event.TurnData{Reason: "periodic"}))
	b.add(event.New(event.Steer, event.ActorNarrator, event.SteerData{Text: steer}))
	b.add(event.New(event.Assistant, event.ActorNarrator, event.AssistantData{ToolCalls: []event.ToolCall{call("n1", "send_message", `{"text":"hi"}`)}, SeenSeq: seen}))
	b.add(event.New(event.ToolResult, event.ActorNarrator, event.ToolResultData{CallID: "n1", Name: "send_message", Output: "delivered"}))
	recorded := Replay(b.events).NarratorView(nil)
	if len(shown) != 1 || len(recorded) != 4 {
		t.Fatalf("shown %d messages, recorded %d", len(shown), len(recorded))
	}
	if shown[0].Text != recorded[0].Text {
		t.Fatalf("the model saw\n%q\nbut the log rebuilds\n%q", shown[0].Text, recorded[0].Text)
	}
	if !strings.Contains(recorded[3].Text, "arrived during the call") {
		t.Fatalf("what arrived during the call belongs after the turn: %q", recorded[3].Text)
	}
	// With nothing observed since its last turn, the steer stands alone.
	alone := WithSteer(recorded[:3], steer)
	if len(alone) != 4 || alone[3].Role != "user" || alone[3].Text != steer {
		t.Fatalf("steer after a turn = %+v", alone)
	}
}

// A trimmed narrator view starts at the cut with a recap of what it told
// the user, and stays put (so its prefix stays cached) until trimmed again.
func TestNarratorViewTrim(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "go"}))
	for i := 0; i < 6; i++ {
		id := "n" + string(rune('a'+i))
		b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "codex", Text: "remark " + id + " " + strings.Repeat("x", 300)}))
		seen := b.seq
		b.add(event.New(event.Assistant, event.ActorNarrator, event.AssistantData{ToolCalls: []event.ToolCall{call(id, "send_message", `{"text":"said `+id+`"}`)}, SeenSeq: seen}))
		b.add(event.New(event.NarratorMessage, event.ActorNarrator, event.NarratorMessageData{Text: "said " + id}))
		b.add(event.New(event.ToolResult, event.ActorNarrator, event.ToolResultData{CallID: id, Name: "send_message", Output: "delivered"}))
	}
	st := Replay(b.events)
	if _, _, ok := st.NarratorTrimPoint(1 << 20); ok {
		t.Fatal("a short view was offered a trim")
	}
	from, recap, ok := st.NarratorTrimPoint(800)
	if !ok || from <= 3 || from >= b.seq {
		t.Fatalf("trim point %d ok=%v", from, ok)
	}
	if !strings.Contains(recap, "no longer shown") || !strings.Contains(recap, "said na") {
		t.Fatalf("recap = %q", recap)
	}
	full := st.NarratorView(nil)
	b.add(event.New(event.NarratorTrim, event.ActorHarness, event.NarratorTrimData{FromSeq: from, Recap: recap}))
	trimmed := Replay(b.events).NarratorView(nil)
	if len(trimmed) >= len(full) || trimmed[0].Role != "user" || !strings.HasPrefix(trimmed[0].Text, recap) {
		t.Fatalf("trimmed view: %d messages (was %d), first %+v", len(trimmed), len(full), trimmed[0])
	}
	var all string
	for _, m := range trimmed {
		all += m.Text
	}
	if strings.Contains(all, "remark na") || !strings.Contains(all, "remark nf") {
		t.Fatal("the trim kept the oldest remark or dropped the newest")
	}
	// New events leave the trimmed prefix as it was.
	b.add(event.New(event.RelayProgress, event.ActorHarness, event.RelayProgressData{Name: "codex", Text: "later"}))
	again := Replay(b.events).NarratorView(nil)
	for i := 0; i < len(trimmed)-1; i++ {
		if again[i].Text != trimmed[i].Text {
			t.Fatalf("message %d changed after an append", i)
		}
	}
}

// Progress checks that said nothing are counted for their tokens only.
func TestNarratorChecksCountTowardTotals(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.NarratorChecks, event.ActorNarrator, event.NarratorChecksData{Count: 3, Usage: event.Usage{Input: 3000, Output: 30, Cached: 2700}}))
	st := Replay(b.events)
	if tot := st.Totals[event.ActorNarrator]; tot.Input != 3000 || tot.Cached != 2700 || st.Calls[event.ActorNarrator] != 3 {
		t.Fatalf("totals %+v, calls %d", tot, st.Calls[event.ActorNarrator])
	}
	for _, m := range st.NarratorView(nil) {
		if strings.Contains(m.Text, "3000") || m.Role == "assistant" {
			t.Fatalf("the count leaked into the narrator's view: %+v", m)
		}
	}
}

// A dismissed question is closed with nothing decided: the orchestrator is
// not woken, an answer is not recorded, and both readers are told in words
// that it was not an answer. The dismissal stands (no asking again) until
// the user writes or the orchestrator yields anew.
func TestDismissClosesQuestionWithoutWaking(t *testing.T) {
	b := newBuilder()
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "paint it"}))                                                        // 3
	b.add(event.New(event.Assistant, event.ActorOrchestrator, event.AssistantData{ToolCalls: []event.ToolCall{call("c1", "yield", `{}`)}, SeenSeq: 3}))  // 4
	b.add(event.New(event.ToolResult, event.ActorOrchestrator, event.ToolResultData{CallID: "c1", Name: "yield", Output: "ok"}))                         // 5
	b.add(event.New(event.Yield, event.ActorOrchestrator, event.YieldData{Done: false, Reason: "waiting for the colour"}))                               // 6
	b.add(event.New(event.NarratorQuestion, event.ActorNarrator, event.NarratorQuestionData{ID: "q1", Text: "Which colour?", Options: []string{"red"}})) // 7
	st := Replay(b.events)
	if st.Question == nil || st.DismissalStands() {
		t.Fatalf("before the dismissal: question=%v stands=%v", st.Question, st.DismissalStands())
	}
	dismiss := b.add(event.New(event.UserDismiss, event.ActorUser, event.UserDismissData{QuestionID: "q1", Source: "finalechat"})) // 8
	st = Replay(b.events)
	if st.Question != nil {
		t.Fatalf("the question is still pending: %+v", st.Question)
	}
	if !st.Idle() {
		t.Fatal("a dismissal must not wake the orchestrator")
	}
	if st.LastUserSeq != 3 {
		t.Fatalf("a dismissal is not something the user said: LastUserSeq = %d", st.LastUserSeq)
	}
	if !st.DismissalStands() {
		t.Fatal("the dismissal should stand while nothing has moved")
	}
	if got := Observe(dismiss, 200); !strings.Contains(got, "USER dismissed q1 without answering") || !strings.Contains(got, "Do not ask it again") {
		t.Fatalf("narrator's view of the dismissal = %q", got)
	}
	view := st.OrchestratorView()
	last := view[len(view)-1].Text
	if !strings.Contains(last, "dismissed question q1 without answering") || !strings.Contains(last, "not an answer and not a go-ahead") {
		t.Fatalf("orchestrator's view ends with %q", last)
	}
	b.add(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "make it green"})) // 9
	st = Replay(b.events)
	if st.DismissalStands() {
		t.Fatal("the dismissal no longer stands once the user has written")
	}
	if st.Idle() {
		t.Fatal("the user's message wakes the orchestrator")
	}
}
