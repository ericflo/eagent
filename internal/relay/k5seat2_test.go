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

// SR4-1. Claude's seat follows the session its SessionStart hook names
// only while it has a transcript to read: the watcher polls Claude (which
// is no sessionStarter) only when tail != nil. Claude writes no file for
// the session /clear starts until its first prompt, so a second /clear (or
// an in-app /resume) before that prompt is never seen: the seat looks for
// the first cleared session's file for ever, and the message typed next,
// which Claude writes to the session it is really on, is never confirmed,
// nor its turn reported.
func TestSr4SeatClaudeSecondClearBeforeAPromptIsNeverFollowed(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	project := filepath.Join(cfg, "projects", "-work")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(project, "a-id.jsonl")
	b := filepath.Join(project, "b-id.jsonl")
	c := filepath.Join(project, "c-id.jsonl")
	writeLines(t, a, claudeTurnLines("sort the shelves", "Sorted.", time.Now().Add(-time.Minute))...)
	state := t.TempDir()
	spool := filepath.Join(state, "after_turn")
	status := filepath.Join(state, "claude-session.json")
	sr4ClaudeHook(t, spool, "a-id", a, "startup")
	s := newSeat(Spec{Spool: spool, StatusFile: status, Hook: "eagent relay-hook"}, claudeAgent{}, &fakeHost{alive: true}, "sr4-clear2-"+filepath.Base(state), "a-id")
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	var seen []string
	until := func(d time.Duration, match func(Event) bool) bool {
		t.Helper()
		deadline := time.After(d)
		for {
			select {
			case ev := <-s.events:
				if ev.Kind == KindInner {
					seen = append(seen, ev.InnerID+" "+filepath.Base(ev.Transcript))
				}
				if match(ev) {
					return true
				}
			case <-deadline:
				return false
			}
		}
	}
	if !until(5*time.Second, func(ev Event) bool { return ev.Kind == KindInner && ev.Transcript == a }) {
		t.Fatalf("never found the first session; moves %q", seen)
	}
	sr4ClaudeHook(t, spool, "b-id", b, "clear") // /clear: no file until a prompt
	if !until(5*time.Second, func(ev Event) bool { return ev.Kind == KindInner && ev.InnerID == "b-id" }) {
		t.Fatalf("never followed the first /clear; moves %q", seen)
	}
	sr4ClaudeHook(t, spool, "c-id", c, "clear") // /clear again before any prompt
	s.track("r1", "run the tests", "queue")
	writeLines(t, c, claudeTurnLines("run the tests", "Ran them.", time.Now())...)
	confirmed := false
	turned := until(3*time.Second, func(ev Event) bool {
		confirmed = confirmed || ev.Kind == KindDelivered && ev.OK && ev.SendID == "r1"
		return ev.Kind == KindTurn && ev.Text == "Ran them."
	})
	if !turned || !confirmed {
		t.Fatalf("after a second /clear: r1 confirmed %v, turn reported %v, moves recorded %q, seat on %q; want the seat on c-id, r1 confirmed and its turn reported", confirmed, turned, seen, s.innerID())
	}
}

// SR4-5. A reattached seat judges the transcript the record read (readIn)
// by the record's mark alone, whenever it reads it (fold: byMark replaces
// the cut by time). Claude's seat now follows Claude to other sessions;
// when Claude comes back to that transcript (/clear, a prompt, then an
// in-app /resume of the conversation it left), the seat reads it again
// from the record's old mark, and every turn it already reported there
// since the reattach is news again: reported twice.
func TestSr4SeatClaudeResumedBackToTheMarkedTranscriptReportsItsTurnsAgain(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	project := filepath.Join(cfg, "projects", "-work")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(project, "a-id.jsonl"), filepath.Join(project, "b-id.jsonl")
	writeLines(t, a, claudeTurnLines("sort the shelves", "Sorted.", time.Now().Add(-time.Minute))...)
	st, _ := os.Stat(a)
	state := t.TempDir()
	spool := filepath.Join(state, "after_turn")
	sr4ClaudeHook(t, spool, "a-id", a, "startup")
	spec := Spec{Dir: state, Spool: spool, StatusFile: filepath.Join(state, "claude-session.json"), Hook: "eagent relay-hook", State: "idle", Read: st.Size()}
	s := attachSeat(spec, claudeAgent{}, &fakeHost{alive: true, screen: claudePromptScreen}, "sr4-back-"+filepath.Base(state), "a-id", a, time.Now().Add(-30*time.Second))
	s.poll = piLook
	go s.watch()
	t.Cleanup(s.Close)
	turns := map[string]int{}
	deadline := time.After(8 * time.Second)
	until := func(text string) {
		t.Helper()
		for turns[text] == 0 {
			select {
			case ev := <-s.events:
				if ev.Kind == KindTurn {
					turns[ev.Text]++
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %q; turns %v", text, turns)
			}
		}
	}
	appendLines := func(path string, lines ...string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			_, _ = f.WriteString(l + "\n")
		}
		f.Close()
	}
	if !eventually(func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.catchingUp }) {
		t.Fatal("never caught up")
	}
	appendLines(a, claudeTurnLines("fix the bug", "Fixed.", time.Now())...)
	until("Fixed.")
	sr4ClaudeHook(t, spool, "b-id", b, "clear")
	if !eventually(func() bool { return s.innerID() == "b-id" }) {
		t.Fatal("never followed /clear")
	}
	appendLines(b, claudeTurnLines("hello", "Hi.", time.Now())...)
	until("Hi.")
	sr4ClaudeHook(t, spool, "a-id", a, "resume") // /resume of the conversation left
	if !eventually(func() bool { return s.innerID() == "a-id" }) {
		t.Fatal("never followed /resume")
	}
	appendLines(a, claudeTurnLines("now test it", "Tested.", time.Now().Add(time.Second))...)
	until("Tested.")
	if turns["Fixed."] != 1 || turns["Sorted."] != 0 {
		t.Fatalf("turns reported %v; want \"Fixed.\" once (it was reported before the /clear) and the history before the mark never", turns)
	}
}

// The reader's suspicion: Claude records the message typed right after
// /clear in the new session before its SessionStart hook says where it is
// (the hook is a process of its own, and Claude can defer it): the looks
// in between still find Claude on the old session, so a cut by time at
// the last such look takes that message for history. A session /clear
// started holds no history: all of it is news.
func TestK5ClaudeMessageRightAfterClearBeforeItsHookIsNews(t *testing.T) {
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
	s := newSeat(Spec{Spool: spool, StatusFile: filepath.Join(state, "claude-session.json"), Hook: "eagent relay-hook"}, claudeAgent{}, &fakeHost{alive: true}, "k5-early-"+filepath.Base(state), "a-id")
	s.state, s.poll = "idle", piLook
	go s.watch()
	t.Cleanup(s.Close)
	var turns []string
	deadline := time.After(5 * time.Second)
	confirmed := false
	until := func(match func(Event) bool) bool {
		for {
			select {
			case ev := <-s.events:
				if ev.Kind == KindTurn {
					turns = append(turns, ev.Text)
				}
				confirmed = confirmed || ev.Kind == KindDelivered && ev.OK && ev.SendID == "r1"
				if match(ev) {
					return true
				}
			case <-deadline:
				return false
			}
		}
	}
	until(func(ev Event) bool { return ev.Kind == KindInner && ev.Transcript == a })
	s.track("r1", "run the tests", "queue")
	// /clear, then the message; looks that still find a-id; then the hook.
	writeLines(t, b, claudeTurnLines("run the tests", "Ran them.", time.Now())...)
	within(looks(4), func() bool { return false })
	sr4ClaudeHook(t, spool, "b-id", b, "clear")
	if !until(func(ev Event) bool { return ev.Kind == KindTurn && ev.Text == "Ran them." }) || !confirmed {
		t.Fatalf("turns %q, r1 confirmed %v; want the message after /clear confirmed and its turn reported", turns, confirmed)
	}
}

// SR4-3. A typed command's delivered event waits for the watcher's next
// look (9888cae) and is dropped when the seat closes first: a restart, a
// stop or eagent's own shutdown within a poll of the typing. The record
// then settles the command as a send whose arrival cannot be confirmed
// (goneUnconfirmed), or, after a resume, as one whose typing "cannot be
// told", though Send typed it and the contract is that typed is delivered.
func TestSr4SeatTypedCommandIsNotDeliveredWhenTheSeatClosesBeforeItsNextLook(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	h := &fakeHost{alive: true, screen: claudePromptScreen}
	s := newSeat(Spec{}, claudeAgent{}, h, "sr4-cmd-close", "id")
	s.state, s.poll = "idle", time.Hour // no look before the close
	go s.watch()
	if _, err := s.Send("c1", "/compact", "queue"); err != nil {
		t.Fatal(err)
	}
	s.Close() // e.g. relay_control restart right after
	delivered := false
	for ev := range s.Events() {
		delivered = delivered || ev.Kind == KindDelivered && ev.SendID == "c1" && ev.OK
	}
	if !delivered {
		t.Fatalf("typed %q, and the seat closed with no delivered for it; want typed to be delivered", k1Typed(h))
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
