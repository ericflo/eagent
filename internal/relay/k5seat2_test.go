package relay

// The sixteenth review's fourth pass over the seat (sr4seat).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sr4ClaudeHook runs what Claude's SessionStart hook runs for spool.
func sr4ClaudeHook(t *testing.T, spool, id, path, source string) {
	t.Helper()
	in, _ := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": id, "transcript_path": path, "source": source})
	var out bytes.Buffer
	if RunStopHook(spool, bytes.NewReader(in), &out) != 0 {
		t.Fatalf("the SessionStart hook failed: %q", out.String())
	}
}

// SR4-2. A reattached seat types nothing until the screen shows the
// agent's own prompt or busy line (35c9fd8), and stays "attached" until
// the agent writes news. Codex's prompt is recognised by its composer's
// placeholder, which is gone while the composer holds text: words the user
// was typing at the terminal, or a send the eagent before typed whose
// Enter was lost (never pressed again once restored). Every send to that
// idle Codex then waits 40 s and fails as a dialog needing an answer; a
// live seat, and main, type it. (The screen is Codex 0.171.0's: its
// composer's footer, "? for shortcuts" and the context left.)
func TestSr4SeatReattachedCodexWithTextInItsComposerRefusesEverySend(t *testing.T) {
	savedWait, savedSettle := readyWait, typeSettle
	readyWait, typeSettle = 300*time.Millisecond, time.Millisecond // the 40 s wait, shortened
	t.Cleanup(func() { readyWait, typeSettle = savedWait, savedSettle })
	dir := t.TempDir()
	transcript := filepath.Join(dir, "rollout.jsonl")
	at := time.Now().Add(-2 * time.Minute)
	writeLines(t, transcript,
		k1CodexMeta(),
		fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"task_started"}}`, stamp(at)),
		fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Fixed."}}`, stamp(at.Add(time.Second))),
	)
	st, _ := os.Stat(transcript)
	// Codex idle at its own prompt, its composer holding a draft.
	h := &fakeHost{alive: true, screen: "› and the README too\n\n  ? for shortcuts                                    100% context left\n"}
	s := attachSeat(Spec{Dir: dir, State: "idle", Read: st.Size()}, codexAgent{}, h, "sr4-codex-draft", "thread-1", transcript, at.Add(2*time.Second))
	s.poll = piLook
	done := make(chan struct{})
	go s.watch()
	go func() {
		defer close(done)
		for range s.Events() {
		}
	}()
	t.Cleanup(func() { s.Close(); <-done })
	if !eventually(func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.catchingUp }) {
		t.Fatal("never caught up")
	}
	res, err := s.Send("s1", "also run the tests", "queue")
	if err != nil || len(k1Typed(h)) != 1 {
		t.Fatalf("Send to an idle reattached Codex: %q, %v, typed %q; want it typed, as a live seat and main type it", res, err, k1Typed(h))
	}
}

// quietOrder collects the seat's moves, deliveries and quiet events until
// a quiet one comes after want, or the deadline.
func quietOrder(t *testing.T, s *Seat, d time.Duration) []string {
	t.Helper()
	var order []string
	deadline := time.After(d)
	for {
		select {
		case ev := <-s.events:
			switch ev.Kind {
			case KindInner:
				order = append(order, "inner "+ev.InnerID)
			case KindDelivered:
				order = append(order, "delivered "+ev.SendID)
			case KindQuiet:
				order = append(order, "quiet")
				if s := s.Snapshot(); !s.Quiet {
					t.Errorf("a quiet event while the status says not quiet")
				}
				return order
			}
		case <-deadline:
			return append(order, "timeout")
		}
	}
}

// The seat says when nothing waits on the agent (Status.Quiet, KindQuiet):
// after a command that moves the session (pi's /new), once the move is
// said and the command's delivered too, not before.
func TestK5QuietAfterPisNewOnceTheMoveIsSaid(t *testing.T) {
	isolatePi(t)
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	dir := t.TempDir()
	first, second, status := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "second.jsonl"), filepath.Join(dir, "pi-status.json")
	writeLines(t, first, piHeaderLine("first", time.Now()))
	writePiStatus(t, status, piStatus{State: "idle", SessionFile: first, SessionID: "first", SessionEntries: entries(0)})
	h := &k1Host{fakeHost: &fakeHost{alive: true}}
	moved := make(chan struct{})
	h.enter = func() error {
		go func() { // pi moves a few looks later
			<-moved
			writeLines(t, second, piHeaderLine("second", time.Now()))
			writePiStatus(t, status, piStatus{State: "idle", SessionFile: second, SessionID: "second", SessionEntries: entries(0)})
		}()
		return nil
	}
	s := newSeat(Spec{StatusFile: status, Dir: dir}, piAgent{name: "pi", label: "Pi", binary: "pi"}, h, "k5q-pi-"+filepath.Base(dir), "first")
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("never quiet at the start: %q", got)
	}
	if _, err := s.Send("c1", "/new", "queue"); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Quiet {
		t.Fatal("quiet with /new just typed")
	}
	within(looks(3), func() bool { return false })
	close(moved)
	got := quietOrder(t, s, 5*time.Second)
	if len(got) != 3 || got[0] != "delivered c1" || got[1] != "inner second" || got[2] != "quiet" {
		t.Fatalf("events %q; want the delivered, the move, then quiet", got)
	}
}

// Claude's /clear: its SessionStart hook (a process of its own) may write
// the new session a while after; quiet waits for that move.
func TestK5QuietAfterClaudesClearWaitsForItsHook(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	project := filepath.Join(cfg, "projects", "-work")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(project, "a-id.jsonl"), filepath.Join(project, "b-id.jsonl")
	writeLines(t, a, claudeTurnLines("sort the shelves", "Sorted.", time.Now().Add(-time.Minute))...)
	state := t.TempDir()
	spool := filepath.Join(state, "after_turn")
	sr4ClaudeHook(t, spool, "a-id", a, "startup")
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	s := newSeat(Spec{Spool: spool, StatusFile: filepath.Join(state, "claude-session.json"), Hook: "eagent relay-hook"}, claudeAgent{}, &fakeHost{alive: true, screen: claudePromptScreen}, "k5q-clear-"+filepath.Base(state), "a-id")
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("never quiet at the start: %q", got)
	}
	if _, err := s.Send("c1", "/clear", "queue"); err != nil {
		t.Fatal(err)
	}
	within(looks(3), func() bool { return false }) // the hook is slow
	sr4ClaudeHook(t, spool, "b-id", b, "clear")
	got := quietOrder(t, s, 5*time.Second)
	if n := len(got); n < 2 || got[n-1] != "quiet" || got[n-2] != "inner b-id" {
		t.Fatalf("events %q; want Claude's move to b-id said before quiet", got)
	}
}

// A command that moves nothing (/compact), typed between two looks: the
// next look sends its delivered and finds the seat quiet again, and says
// so after the delivered, though the seat was quiet before the typing too.
func TestK5QuietIsSaidAfterACommandsDeliveredInTheSameLook(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true, screen: claudePromptScreen}, "k5q-compact", "id")
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	if got := quietOrder(t, s, 5*time.Second); got[len(got)-1] != "quiet" {
		t.Fatalf("never quiet at the start: %q", got)
	}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("c%d", i)
		if _, err := s.Send(id, "/compact", "queue"); err != nil {
			t.Fatal(err)
		}
		if got := quietOrder(t, s, 5*time.Second); len(got) != 2 || got[0] != "delivered "+id || got[1] != "quiet" {
			t.Fatalf("events %q; want the delivered, then quiet", got)
		}
	}
}

// A send the seat waits to confirm keeps it from quiet until confirmed.
func TestK5QuietWaitsForASendToBeConfirmed(t *testing.T) {
	s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "k5q-send", "")
	s.state, s.poll = "idle", piLook
	s.track("r1", "run the tests", "queue")
	go s.watch()
	t.Cleanup(s.Close)
	within(looks(3), func() bool { return false })
	if s.Snapshot().Quiet {
		t.Fatal("quiet with a send to confirm")
	}
	s.fold(obs{kind: "user", text: "run the tests"})
	if got := quietOrder(t, s, 5*time.Second); len(got) != 2 || got[0] != "delivered r1" || got[1] != "quiet" {
		t.Fatalf("events %q; want r1 confirmed, then quiet", got)
	}
}
