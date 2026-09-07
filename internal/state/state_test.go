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
