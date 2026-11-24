package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/tools"
)

func TestScopedToolLists(t *testing.T) {
	baseOrch := tools.OrchestratorToolsFor(false, false)
	if !tools.Has(baseOrch, "relay_start") || !tools.Has(baseOrch, "retitle_thread") || !tools.Has(baseOrch, "bash") {
		t.Fatalf("base orchestrator missing core tools")
	}
	for _, excluded := range []string{"relay_forward", "relay_send", "relay_status", "relay_screen", "relay_control", "relay_autopilot", "session_list", "session_read", "session_search"} {
		if tools.Has(baseOrch, excluded) {
			t.Fatalf("non-relay pre-rollover orchestrator should omit %q", excluded)
		}
	}

	relayOrch := tools.OrchestratorToolsFor(true, false)
	for _, want := range []string{"relay_start", "relay_forward", "relay_send", "relay_status", "relay_screen", "relay_control", "relay_autopilot"} {
		if !tools.Has(relayOrch, want) {
			t.Fatalf("relay orchestrator missing %q", want)
		}
	}
	if tools.Has(relayOrch, "session_list") {
		t.Fatal("pre-rollover relay orchestrator should still omit session_list")
	}

	rolledOrch := tools.OrchestratorToolsFor(false, true)
	for _, want := range []string{"session_list", "session_read", "session_search"} {
		if !tools.Has(rolledOrch, want) {
			t.Fatalf("post-rollover orchestrator missing %q", want)
		}
	}

	workTask := tools.TaskToolsFor("work")
	if tools.Has(workTask, "session_list") || tools.Has(workTask, "session_read") || tools.Has(workTask, "session_search") {
		t.Fatal("regular work task should omit session archive tools")
	}
	dossierTask := tools.TaskToolsFor("dossier")
	for _, want := range []string{"session_list", "session_read", "session_search", "complete_task"} {
		if !tools.Has(dossierTask, want) {
			t.Fatalf("dossier task missing %q", want)
		}
	}
}

func TestNarratorSystemPromptStaticAcrossPhoneRemoteMode(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Prompt: "hello"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	sysBefore := rt.narratorSystem(PhoneStatus(), "")
	sysAfter := rt.narratorSystem(PhoneStatus(), "")
	if sysBefore != sysAfter {
		t.Fatal("narrator system prompt must remain byte-identical")
	}
	if strings.Contains(sysBefore, "remote mode") {
		t.Fatalf("remote mode should not live in static system prompt: %q", sysBefore)
	}
	// PhoneThread events must not bump narrWorthy or falsely wake the narrator.
	ev := event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "t1", RemoteMode: true})
	ev.Seq = 42
	rt.noteWake(ev)
	if rt.narrWorthy == 42 {
		t.Fatal("PhoneThread must not bump narrWorthy")
	}
}

func TestRoutineSteersOmittedOnMidTurnLoops(t *testing.T) {
	now := time.Now()
	st := state.New()
	// Routine wake & routine mid-turn with nothing actionable produce empty steer.
	if s := steerOrchestrator(st, now, 10_000, 120_000, "user message", 1, 30, "/proj", "/proj", "/proj", false); s != "" {
		t.Fatalf("routine wake should omit steer, got %q", s)
	}
	if s := steerOrchestrator(st, now, 10_000, 120_000, "", 2, 30, "/proj", "/proj", "/proj", false); s != "" {
		t.Fatalf("routine mid-turn should omit steer, got %q", s)
	}
	// Mid-turn cd emits Working directory once, then stays silent while unchanged.
	if s := steerOrchestrator(st, now, 10_000, 120_000, "", 2, 30, "/proj/sub", "/proj", "/proj", false); !strings.Contains(s, "Working directory: /proj/sub") {
		t.Fatalf("mid-turn cd should emit working directory, got %q", s)
	}
	if s := steerOrchestrator(st, now, 10_000, 120_000, "", 3, 30, "/proj/sub", "/proj/sub", "/proj", false); s != "" {
		t.Fatalf("unchanged mid-turn cwd should omit steer, got %q", s)
	}
	// Context warning appears only at >= 70% of rolloverTokens.
	if s := steerOrchestrator(st, now, 80_000, 120_000, "", 2, 30, "/proj", "/proj", "/proj", false); s != "" {
		t.Fatalf("below 70%% rollover should omit context steer, got %q", s)
	}
	if s := steerOrchestrator(st, now, 90_000, 120_000, "", 2, 30, "/proj", "/proj", "/proj", false); !strings.Contains(s, "Context: 90k of 120k") {
		t.Fatalf("above 70%% rollover should emit context steer, got %q", s)
	}

	// Task worker omits routine steers until <= 10 turns remain.
	task := &state.Task{ID: "t1"}
	if s := steerTask(task, 1, 80, now, "/proj", "/proj"); s != "" {
		t.Fatalf("routine task turn should omit steer, got %q", s)
	}
	if s := steerTask(task, 70, 80, now, "/proj", "/proj"); !strings.Contains(s, "Only 10 calls remain") {
		t.Fatalf("low-turn task steer missing warning: %q", s)
	}
	if s := steerTask(task, 80, 80, now, "/proj", "/proj"); !strings.Contains(s, "THIS IS YOUR LAST CALL") {
		t.Fatalf("last-turn task steer missing warning: %q", s)
	}
}

func TestNonRelayNarratorTrimsAtNonRelayThreshold(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Prompt: "Audit prefix caching"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	oldKeep := narratorKeepChars
	narratorKeepChars = 800
	t.Cleanup(func() { narratorKeepChars = oldKeep })

	rt.append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "Audit prefix caching"}))
	for i := 0; i < 6; i++ {
		rt.append(event.New(event.Note, event.ActorOrchestrator, event.NoteData{Text: strings.Repeat("x", 400)}))
	}
	// Below non-relay threshold (60k) => no trim.
	rt.maybeTrimNarrator(50_000)
	if rt.st.NarratorTrim != nil {
		t.Fatal("non-relay narrator trimmed below threshold")
	}
	// At non-relay threshold => trims and keeps opening request in recap.
	rt.maybeTrimNarrator(narratorTrimAtNonRelay)
	if rt.st.NarratorTrim == nil {
		t.Fatal("non-relay narrator should trim at threshold")
	}
	if !strings.Contains(rt.st.NarratorTrim.Recap, "Original user request: Audit prefix caching.") {
		t.Fatalf("recap missing original request: %q", rt.st.NarratorTrim.Recap)
	}
}
