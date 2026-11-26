package relay

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// A message longer than the part of it that is compared still arrives: the
// transcript shows it whole, and its send is confirmed. Unconfirmed, the
// seat pressed Enter at it twice after the turn and reported it lost 45 s
// later.
func TestALongSendIsConfirmedWhenItArrives(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("Please read the error below and fix the failing test. ", 6) + "\nThe log ends: FAIL"
	if len(long) <= 160 {
		t.Fatal("the message must be longer than the compared part")
	}
	h := &fakeHost{alive: true, screen: "> " + long + "\n\n  bypass permissions on (shift+tab to cycle)"}
	s := newSeat(Spec{Name: "claude", Profile: config.RelayProfile{Agent: "claude"}}, claudeAgent{}, h, "eagent-1-claude", "")
	s.state = "idle"
	s.track("r1", long, "queue")
	s.fold(obs{kind: "user", text: long})
	s.fold(obs{kind: "working"})
	s.fold(obs{kind: "turn", text: "Fixed."})
	start := time.Now()
	for _, after := range []time.Duration{7, 14, 21, 60, 120} {
		s.checkScreen(start.Add(after * time.Second))
	}
	var delivered []Event
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered {
			delivered = append(delivered, ev)
		}
	}
	if len(delivered) != 1 || !delivered[0].OK || delivered[0].SendID != "r1" {
		t.Fatalf("deliveries %+v, want r1 confirmed once", delivered)
	}
	if slices.Contains(h.keys, "Enter") {
		t.Fatalf("pressed %q at a message that had arrived", h.keys)
	}
}

// A transcript that holds only the start of a long message (a prefix of
// it) still confirms it, as it did for short ones.
func TestALongSendIsConfirmedByAPrefixOfIt(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("abcdefghij ", 30)
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
	s.track("r1", long, "queue")
	if ids := s.matchPending(long[:200]); len(ids) != 1 {
		t.Fatalf("a 200-byte prefix confirmed %v", ids)
	}
}

// A working agent whose screen always shows its status line (Claude's
// footer, Antigravity's relay line) and has written nothing for ten
// minutes is reported blocked, and stays so while its screen says it is
// still busy: the status line alone does not make it idle.
func TestAQuietAgentStaysBlockedWhileItsScreenSaysItIsBusy(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		agent      Agent
		busy, idle string
	}{
		{claudeAgent{}, "✻ Thinking… (10m 12s · esc to interrupt)\n  ⏵⏵ bypass permissions on (shift+tab to cycle)", "> \n  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		{codexAgent{}, "• Working (10m 3s • esc to interrupt)\n› Ask Codex to do anything", "› Ask Codex to do anything\n  ? for shortcuts"},
		{antigravityAgent{}, "thinking\nAntigravity relay · running · Gemini 3.8", "> \nAntigravity relay · idle · Gemini 3.8"},
	} {
		t.Run(c.agent.Name(), func(t *testing.T) {
			h := &fakeHost{alive: true, screen: c.busy}
			s := newSeat(Spec{Name: c.agent.Name()}, c.agent, h, "x", "")
			start := time.Now()
			s.state, s.stateAt, s.lastLine = "working", start.Add(-11*time.Minute), start.Add(-11*time.Minute)
			s.checkScreen(start)
			if st := s.Snapshot().State; st != "blocked" {
				t.Fatalf("after ten quiet minutes the seat is %s", st)
			}
			for i := 1; i <= 4; i++ {
				s.checkScreen(start.Add(time.Duration(i) * 1500 * time.Millisecond))
			}
			if st := s.Snapshot().State; st != "blocked" {
				t.Fatalf("a busy screen with its status line made the seat %s", st)
			}
			h.mu.Lock()
			h.screen = c.idle
			h.mu.Unlock()
			s.checkScreen(start.Add(9 * time.Second))
			if st := s.Snapshot().State; st != "idle" {
				t.Fatalf("at its prompt again the seat is %s", st)
			}
		})
	}
}

// A quiet agent reported blocked still takes a message: it is typed in as
// into a working agent, as it was when the seat went back to idle.
func TestAQuietBusyAgentStillTakesAMessage(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true, screen: "✻ Thinking… (10m 12s · esc to interrupt)\n  ⏵⏵ bypass permissions on"}
	s := newSeat(Spec{Name: "claude"}, claudeAgent{}, h, "x", "")
	start := time.Now()
	s.state, s.stateAt, s.lastLine = "working", start.Add(-11*time.Minute), start.Add(-11*time.Minute)
	s.checkScreen(start)
	if st := s.Snapshot().State; st != "blocked" {
		t.Fatalf("seat is %s", st)
	}
	msg, err := s.Send("r1", "status?", "queue")
	if err != nil || !strings.Contains(msg, "while it works") {
		t.Fatalf("send: %q %v", msg, err)
	}
}

// A seat answers only its own agent's dialogs and reads only its own
// agent's prompt: text that another agent would show (here, on a Codex
// screen, Claude's trust question and its "for shortcuts" hint) is not
// pressed at and does not make it ready.
func TestASeatAnswersOnlyItsOwnAgentsDialogs(t *testing.T) {
	t.Parallel()
	h := &fakeHost{alive: true, screen: "  grep: claude.go: Yes, I trust this folder\n  ? for shortcuts"}
	s := newSeat(Spec{Name: "codex"}, codexAgent{}, h, "x", "")
	start := time.Now()
	s.checkScreen(start)
	s.checkScreen(start.Add(1500 * time.Millisecond))
	if len(h.keys) != 0 {
		t.Fatalf("a Codex seat pressed %q at Claude's dialog text", h.keys)
	}
	if st := s.Snapshot().State; st != "starting" {
		t.Fatalf("Claude's hint made a Codex seat %s", st)
	}
	// Its own trust prompt is still answered.
	h.mu.Lock()
	h.screen = "Trust this folder?\n› 1. Trust and continue"
	h.mu.Unlock()
	s.checkScreen(start.Add(5 * time.Second))
	if strings.Join(h.keys, " ") != "Enter" {
		t.Fatalf("keys for Codex's own trust prompt %q", h.keys)
	}
}

// A seat reattached to an agent that is in the middle of a turn says it is
// working, from the transcript it re-reads; one reattached after the turn
// ended says idle. Nothing already on the record is reported again.
func TestAReattachedSeatKnowsTheAgentIsMidTurn(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		hist []obs
		want string
	}{
		{"mid-turn", []obs{{kind: "user", text: "build it"}, {kind: "working"}, {kind: "activity", text: "running make", tool: true}}, "working"},
		{"turn over", []obs{{kind: "user", text: "build it"}, {kind: "working"}, {kind: "turn", text: "Built."}}, "idle"},
		{"interrupted", []obs{{kind: "working"}, {kind: "aborted"}}, "idle"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
			s.markAttached(since)
			for i, o := range c.hist {
				o.at = since.Add(time.Duration(i-len(c.hist)) * time.Second)
				s.fold(o)
			}
			if st := s.Snapshot().State; st != c.want {
				t.Fatalf("reattached seat is %s, want %s", st, c.want)
			}
			for _, ev := range drain(s.events) {
				if ev.Kind != KindState {
					t.Fatalf("history reported again: %+v", ev)
				}
			}
			// The turn that goes on ends as news.
			s.fold(obs{kind: "turn", text: "Done.", at: since.Add(time.Second)})
			var turns int
			for _, ev := range drain(s.events) {
				if ev.Kind == KindTurn {
					turns++
				}
			}
			if turns != 1 || s.Snapshot().State != "idle" {
				t.Fatalf("turns %d, state %s", turns, s.Snapshot().State)
			}
		})
	}
}

// A message held for the end of Claude's turn is typed in instead when no
// Stop hook would hand it over (eagent runs under another name).
func TestClaudeWithoutItsStopHookTypesAHeldMessage(t *testing.T) {
	t.Parallel()
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "claude", Spool: spool}, claudeAgent{}, h, "x", "")
	s.state = "working"
	msg, err := s.Send("r1", "after this, run the tests", "after_turn")
	if err != nil || strings.Contains(msg, "held for") {
		t.Fatalf("send: %q %v", msg, err)
	}
	if len(h.typed) != 1 {
		t.Fatalf("typed %q", h.typed)
	}
	if names, _ := filepath.Glob(filepath.Join(spool, "*")); len(names) != 0 {
		t.Fatalf("spooled %v with no hook to read them", names)
	}
}

// A message still held when Claude's turn has ended without its Stop hook
// taking it (an interrupt) is reported undelivered heldGrace after the
// transcript showed the turn over, saying why; it is never typed, and it
// leaves the spool.
func TestClaudeLeftOverAfterAnInterruptIsReportedUndelivered(t *testing.T) {
	saved := heldGrace
	heldGrace = 200 * time.Millisecond
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true, screen: claudePromptScreen}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	s.state = "working"
	if msg, err := s.Send("r1", "then run the tests", "after_turn"); err != nil || !strings.Contains(msg, "held for") {
		t.Fatalf("send: %q %v", msg, err)
	}
	s.fold(obs{kind: "aborted"}) // no Stop hook runs on an interrupt
	drain(s.events)
	start := time.Now()
	s.reportLeftOvers(start)
	if evs := drain(s.events); len(evs) != 0 {
		t.Fatalf("reported within heldGrace: %+v", evs)
	}
	s.reportLeftOvers(start.Add(heldGrace))
	var lost []Event
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && !ev.OK {
			lost = append(lost, ev)
		}
	}
	if len(lost) != 1 || lost[0].SendID != "r1" || !strings.Contains(lost[0].Detail, "turn ended before it took this message") {
		t.Fatalf("reports %+v", lost)
	}
	if names, _ := filepath.Glob(filepath.Join(spool, "*")); len(names) != 0 || len(h.typed) != 0 || len(s.Snapshot().Pending) != 0 {
		t.Fatalf("spool %v, typed %q, pending %v", names, h.typed, s.Snapshot().Pending)
	}
}

// A held message the Stop hook took is not reported after the turn.
func TestClaudeHeldMessageTakenByTheHookIsNotReported(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, h, "x", "")
	if err := spoolMessage(spool, "r1", "held"); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	RunStopHook(spool, strings.NewReader("{}"), &out)
	s.state = "working"
	s.fold(obs{kind: "turn"})
	drain(s.events)
	s.reportLeftOvers(time.Now().Add(time.Second))
	if evs := drain(s.events); len(evs) != 0 || !strings.Contains(out.String(), "held") {
		t.Fatalf("events %+v, hook said %q", evs, out.String())
	}
}

// A Claude relay restarted (or given another model) before its first
// message has no conversation to resume: Claude exits on --resume of an
// id it never saved. It starts on the same id instead, and resumes once
// the conversation exists.
func TestClaudeResumesOnlyAConversationItSaved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	id := "0b8f6a52-6d1e-4c8e-9f55-3f2d8b8f1a11"
	spec := Spec{Profile: config.RelayProfile{Agent: "claude"}}
	argv, resumed := launchCommand(claudeAgent{}, spec, id, true)
	if resumed || !slices.Contains(argv, "--session-id") || slices.Contains(argv, "--resume") {
		t.Fatalf("a never-used session starts as %q (resumed %v)", argv, resumed)
	}
	if i := slices.Index(argv, "--session-id"); argv[i+1] != id {
		t.Fatalf("argv %q lost the id", argv)
	}
	dir := filepath.Join(home, "projects", "-w")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	argv, resumed = launchCommand(claudeAgent{}, spec, id, true)
	if !resumed || !slices.Contains(argv, "--resume") {
		t.Fatalf("a saved session starts as %q (resumed %v)", argv, resumed)
	}
	// Other agents resume as they did.
	if _, resumed := launchCommand(codexAgent{}, Spec{}, "x", true); !resumed {
		t.Fatal("codex no longer resumes")
	}
}
