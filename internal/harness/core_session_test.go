package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// Ports of the session-lifecycle tests of the old harness_test.go to the
// one loop. Each names what the old test protected.

// coreExit waits for a session's run to return and leaves the exit code
// where session.stop will find it.
func coreExit(s *session) int {
	s.t.Helper()
	select {
	case code := <-s.done:
		s.done <- code
		return code
	case <-time.After(20 * time.Second):
		s.t.Fatal("the session did not end")
	}
	return -1
}

// coreResume resumes a stopped session's log with a fresh seat and the same
// model, interactively.
func coreResume(t *testing.T, path, project string, model *scripted, seat *fakeSeat) (*session, *[]string) {
	t.Helper()
	_, attached := useFakeRelays(t, seat)
	ui := &fakeUI{input: make(chan string, 8)}
	rt, err := Resume(testConfig(model.srv.URL), Options{Project: project, Interactive: true}, ui, path)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, rt: rt, ui: ui, seat: seat, model: model, done: make(chan int, 1)}
	ctx, cancel := contextWithCancel()
	s.cancel = cancel
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	return s, attached
}

// stopsAndReports is what a model does at the agent's turn end in a batch
// session that was told to stop when done.
func stopsAndReports(text string) reply {
	return reply{calls: []event.ToolCall{
		tc("send_message", `{"text":"`+text+`","important":true}`),
		tc("relay_control", `{"action":"stop","reason":"the work is done"}`),
	}}
}

// Old TestEndToEndDelegationAndFinish: a batch session runs the work to its
// end, the user hears how it ended, the session ends "done" with exit 0,
// the log replays to the same picture, and the model is not called without
// something new to see. (Delegation to task workers is gone with them; an
// agent does the work.)
func TestCoreBatchSessionFinishesAndReplaysToTheSameState(t *testing.T) {
	rt, ui, seat := batch(t, brief+" Stop when done.", "Done: #42 and #44 are merged.", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
			return stopsAndReports("Done: Codex merged #42 and #44 on main.")
		}
		return hold()
	})
	if got := seat.sentTexts(); len(got) != 1 || got[0] != brief+" Stop when done." {
		t.Fatalf("the agent was sent %q", got)
	}
	said := ui.said()
	if len(said) == 0 || !strings.HasPrefix(said[len(said)-1], "Done:") {
		t.Fatalf("said %q", said)
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	st := state.Replay(evs)
	if !st.Ended || st.EndReason != "done" || len(st.ActiveRelays()) != 0 || st.Question != nil {
		t.Fatalf("replayed: ended=%v reason=%q relays=%d", st.Ended, st.EndReason, len(st.ActiveRelays()))
	}
	if st.LastSeq() != rt.st.LastSeq() || st.Title != rt.st.Title || st.Totals[event.ActorEagent] != rt.st.Totals[event.ActorEagent] {
		t.Fatalf("the replay differs from the live state: seq %d vs %d", st.LastSeq(), rt.st.LastSeq())
	}
	// At most: the passed message, the turn's end, the closing look.
	if n := len(rt.st.Events); n == 0 {
		t.Fatal("no events")
	}
	var calls int
	for _, ev := range evs {
		switch ev.Type {
		case event.Assistant:
			calls++
		case event.Checks:
			var d event.ChecksData
			_ = ev.Decode(&d)
			calls += d.Count
		}
	}
	if calls > 4 || calls < 2 {
		t.Fatalf("the model was called %d times for one turn of work", calls)
	}
}

// Old TestInterruptAndResume: an interrupt ends the run with exit 130 and
// "interrupted" on the record, whatever was in flight; resuming the log
// counts a resume, picks the agent up again, and what the interrupted call
// was looking at is still owed a look and gets one. (The task worker that
// was interrupted is gone with task workers.)
func TestCoreInterruptEndsTheRunAndResumePicksUpTheOwedLook(t *testing.T) {
	var resumed atomic.Bool
	var blocked atomic.Int32
	never := make(chan struct{})
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
			if !resumed.Load() {
				blocked.Add(1)
				return reply{block: never, calls: []event.ToolCall{tc("hold", `{}`)}}
			}
			return say("Codex merged #42 and #44; main is green at c92b5bad.")
		}
		return hold()
	}, nil)
	waitFor(t, "the look at the turn's end to be in flight", func() bool { return blocked.Load() > 0 })
	s.cancel()
	if code := coreExit(s); code != 130 {
		t.Fatalf("interrupted exit = %d", code)
	}
	path, project := s.rt.sess.Path, s.rt.opts.Project
	st := state.Replay(mustReadCore(t, path))
	if st.EndReason != "interrupted" || len(st.ActiveRelays()) != 1 {
		t.Fatalf("after interrupt: reason=%q relays=%d", st.EndReason, len(st.ActiveRelays()))
	}
	if ui := s.ui.said(); len(ui) != 0 {
		t.Fatalf("said %q before the interrupt", ui)
	}

	resumed.Store(true)
	s2, attached := coreResume(t, path, project, s.model, newFakeSeat("unused"))
	s2.waitFor("the owed report after the resume", func() bool { return s2.ui.messageCount() == 1 })
	if got := s2.ui.said()[0]; got != "Codex merged #42 and #44; main is green at c92b5bad." {
		t.Fatalf("said %q", got)
	}
	if len(*attached) != 1 {
		t.Fatalf("reattached %d agents", len(*attached))
	}
	var resumes int
	s2.read(func(st *state.State) { resumes = st.Resumes })
	if resumes != 1 {
		t.Fatalf("resumes = %d", resumes)
	}
}

// Old TestInteractiveQuestionAndAnswer: a question waits for the answer; an
// answer by option number is recorded as the option's text against the
// question; the question is then closed; /quit ends the session "quit"
// with exit 0.
func TestCoreQuestionWaitsForTheAnswerAndQuitEnds(t *testing.T) {
	var mu sync.Mutex
	asked := false
	s := startSession(t, "paint it", func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		if msgs[len(msgs)-1]["role"] == "tool" {
			return hold()
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(tail, "The user answered your question"):
			return reply{calls: []event.ToolCall{tc("send_message", `{"text":"Blue it is."}`), tc("relay_send", `{"text":"Paint it blue."}`)}}
		case strings.Contains(tail, "finished a turn") && !asked:
			asked = true
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Which colour?","options":["red","blue"]}`)}}
		}
		return hold()
	}, nil)
	s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
	s.settled()
	if _, a, _ := s.ui.snapshot(); a[0] != "Which colour?" {
		t.Fatalf("asked %q", a)
	}
	s.type_("2")
	s.waitFor("the answer to be acted on", func() bool { return s.ui.messageCount() == 1 && len(s.seat.sentTexts()) == 2 })
	s.settled()
	if got := s.ui.said()[0]; got != "Blue it is." {
		t.Fatalf("said %q", got)
	}
	if got := s.seat.sentTexts()[1]; got != "Paint it blue." {
		t.Fatalf("the agent was sent %q", got)
	}
	s.type_("/quit")
	if code := coreExit(s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	evs := mustReadCore(t, s.rt.sess.Path)
	st := state.Replay(evs)
	if st.EndReason != "quit" || st.Question != nil {
		t.Fatalf("end=%q question=%v", st.EndReason, st.Question)
	}
	answered := false
	for _, ev := range evs {
		if ev.Type == event.UserAnswer {
			var d event.UserAnswerData
			_ = ev.Decode(&d)
			answered = d.Text == "blue" && d.QuestionID == "q1"
		}
	}
	if !answered {
		t.Fatal("the numbered option was not recorded as its text against the question")
	}
}

// Old TestWakeDuringYieldingCallIsNotLost: something that happens while a
// model call is in flight is not lost when the call comes back having seen
// only what came before it: it gets a look of its own.
func TestCoreWhatHappensDuringACallIsLookedAtAfterIt(t *testing.T) {
	release := make(chan struct{})
	var blocked atomic.Int32
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		if msgs[len(msgs)-1]["role"] == "tool" || !strings.Contains(tail, "finished a turn") {
			return hold()
		}
		if strings.Contains(allText(msgs), "#45 conflicts with main") {
			return say("Codex found that #45 conflicts with main.")
		}
		if blocked.Add(1) == 1 {
			return reply{block: release, calls: []event.ToolCall{tc("hold", `{}`)}}
		}
		return hold()
	}, nil)
	waitFor(t, "the first look to be in flight", func() bool { return blocked.Load() == 1 })
	s.seat.emit(
		relay.Event{Kind: relay.KindState, State: "working"},
		relay.Event{Kind: relay.KindTurn, Text: "#45 conflicts with main.", DurationMS: 1000},
		relay.Event{Kind: relay.KindState, State: "idle"},
	)
	s.waitFor("the second turn on the record", func() bool { return len(s.events(event.RelayTurn)) == 2 })
	close(release)
	s.waitFor("the second turn to be told", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	var seen int64
	s.rt.sync(func() { seen = s.rt.turns.seen })
	if last := s.events(event.RelayTurn)[1]; seen < last.Seq {
		t.Fatalf("the second turn (seq %d) was never looked at (seen %d)", last.Seq, seen)
	}
}

// Old TestResumeRepairsTornLog: a log torn by a crash mid-write is repaired
// on resume (the torn bytes kept beside it), the sequence carries on with
// no gap, and the session works again.
func TestCoreResumeRepairsATornLog(t *testing.T) {
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			return say("Here.")
		}
		return hold()
	}, nil)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()
	files, _ := store.ReadOnly(path).Files()
	last := filepath.Join(path, files[len(files)-1])
	f, err := os.OpenFile(last, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"seq":999,"type":"user.message","actor":"user","data":{"text":"trunc`)
	f.Close()

	s2, _ := coreResume(t, path, project, s.model, newFakeSeat("unused"))
	s2.type_("@e are you there?")
	s2.waitFor("the answer after the repair", func() bool { return s2.ui.messageCount() == 1 })
	s2.settled()
	s2.stop()
	evs, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	for _, ev := range evs {
		if ev.Seq != seq+1 {
			t.Fatalf("sequence gap after the repair: %d after %d", ev.Seq, seq)
		}
		seq = ev.Seq
	}
	if _, err := os.Stat(last + ".torn"); err != nil {
		t.Fatal("the torn bytes were not kept")
	}
}

// coreSeats makes every agent a session starts a seat of its own, by
// session, for tests that run two sessions at once.
func coreSeats(t *testing.T, reply string) func(session string) *fakeSeat {
	t.Helper()
	var mu sync.Mutex
	seats := map[string]*fakeSeat{}
	get := func(session string) *fakeSeat {
		mu.Lock()
		defer mu.Unlock()
		if seats[session] == nil {
			seats[session] = newFakeSeat(reply)
		}
		return seats[session]
	}
	oldLaunch, oldAttach := launchRelay, attachRelay
	launchRelay = func(spec relay.Spec, innerID string, resume bool) (relaySeat, relay.Started, error) {
		return get(spec.Session), relay.Started{Host: "tmux", Target: relay.Target(spec.Session, spec.Name), InnerID: innerID, Command: []string{spec.Profile.Agent}}, nil
	}
	t.Cleanup(func() { launchRelay, attachRelay = oldLaunch, oldAttach })
	return get
}

// Old TestTwoSessionsInOneProject: two sessions in one project run side by
// side, each with its own id, its own agent and its own log, and both end.
func TestCoreTwoSessionsInOneProject(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seats := coreSeats(t, "Done.")
	model := newScripted(func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
			return stopsAndReports("Done.")
		}
		return hold()
	})
	defer model.srv.Close()
	project := t.TempDir()
	cfg := testConfig(model.srv.URL)
	rtA, err := New(cfg, Options{Project: project, Prompt: "write a. Stop when done."}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	rtB, err := New(cfg, Options{Project: project, Prompt: "write b. Stop when done."}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	if rtA.SessionID() == rtB.SessionID() {
		t.Fatal("two sessions got the same id")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	codes := make(chan int, 2)
	go func() { codes <- rtA.Run(ctx) }()
	go func() { codes <- rtB.Run(ctx) }()
	if a, b := <-codes, <-codes; a != 0 || b != 0 {
		t.Fatalf("exit codes %d %d", a, b)
	}
	for id, want := range map[string]string{rtA.SessionID(): "write a. Stop when done.", rtB.SessionID(): "write b. Stop when done."} {
		if got := seats(id).sentTexts(); len(got) != 1 || got[0] != want {
			t.Fatalf("session %s's agent was sent %q", id, got)
		}
	}
	infos, _ := store.List(store.Root(project))
	if len(infos) != 2 {
		t.Fatalf("expected 2 sessions, found %d", len(infos))
	}
	for _, info := range infos {
		if st := state.Replay(mustReadCore(t, info.Path)); st.EndReason != "done" {
			t.Fatalf("session %s ended %q", info.ID, st.EndReason)
		}
	}
}

// Old TestCleanNarrationStripsLeakedTokens: provider control markup that
// escapes into a message is removed before the user sees it; ordinary text
// is left alone.
func TestCoreCleanNarrationStripsLeakedTokens(t *testing.T) {
	if got := cleanNarration("Done. The file is in place.</｜DSML｜parameter>"); got != "Done. The file is in place." {
		t.Fatalf("got %q", got)
	}
	if got := cleanNarration("ok<|im_end|>"); got != "ok" {
		t.Fatalf("got %q", got)
	}
	if got := cleanNarration("a < b and b > c"); got != "a < b and b > c" {
		t.Fatalf("ordinary text altered: %q", got)
	}
}

// Old TestStartDirIsTheFirstWorkingDirectory: a session started somewhere
// other than the project root works there from the start (the first
// command ran there; now the agent is started there), while the project
// stays the root.
func TestCoreStartDirIsWhereTheAgentStarts(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	launched, _ := useFakeRelays(t, newFakeSeat("ok"))
	project, elsewhere := t.TempDir(), t.TempDir()
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: project, Prompt: "where are you", StartDir: elsewhere}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	if len(*launched) != 1 || !sameDir((*launched)[0].Dir, elsewhere) {
		t.Fatalf("the agent was started in %v, want %s", *launched, elsewhere)
	}
	st := state.Replay(mustReadCore(t, rt.sess.Path))
	if !sameDir(st.WorkDir(), elsewhere) || st.Cwd != project {
		t.Fatalf("workdir=%q project=%q", st.WorkDir(), st.Cwd)
	}
}

// Old TestNarratorAnswersWithTheOrchestratorNotAheadOfIt: a question the
// user puts to the worker is answered with the worker's answer, never a
// guess. When the answer comes quickly the user hears it alone; when it is
// slow they hear one line that the message landed, then the answer once it
// comes. (The narrator's grace period is now the loop's wake settle, which
// reads a message and a quick answer in one call.)
func TestCoreAnswerFollowsTheAgentNotAheadOfIt(t *testing.T) {
	old := wakeSettle
	wakeSettle = 300 * time.Millisecond
	defer func() { wakeSettle = old }()
	var seen tails
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		if msgs[len(msgs)-1]["role"] == "tool" {
			return hold()
		}
		seen.add(tail)
		all := allText(msgs)
		switch {
		case strings.Contains(tail, "finished a turn"):
			if strings.Contains(all, "Slowly: yes.") {
				return say("Slowly: yes.")
			}
			return say("No, only the summary.")
		case strings.Contains(tail, "Say in one short line that it reached Codex"):
			return say("Got it, checking.")
		}
		return hold()
	}, nil)
	s.seat.mu.Lock()
	s.seat.reply = "No, only the summary."
	s.seat.mu.Unlock()

	// Quick: the message and the answer are read in one call.
	s.type_("did you read the file?")
	s.waitFor("the quick answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	if m := s.ui.said(); len(m) != 1 || m[0] != "No, only the summary." {
		t.Fatalf("messages after a quick answer = %q", m)
	}
	if tail := seen.find("finished a turn"); !strings.Contains(tail, "typed into Codex's terminal as written") || !strings.Contains(tail, "Hold instead if Codex has already answered it") {
		t.Fatalf("the quick answer's call did not see both:\n%s", tail)
	}

	// Slow: a line that it landed, then the answer.
	s.seat.mu.Lock()
	s.seat.hang = true
	s.seat.mu.Unlock()
	s.type_("slow question")
	s.waitFor("the acknowledgement", func() bool { return s.ui.messageCount() == 2 })
	s.settled()
	if m := s.ui.said(); m[1] != "Got it, checking." {
		t.Fatalf("messages while the agent is slow = %q", m)
	}
	s.seat.emit(
		relay.Event{Kind: relay.KindTurn, Text: "Slowly: yes.", DurationMS: 4000},
		relay.Event{Kind: relay.KindState, State: "idle"},
	)
	s.waitFor("the slow answer", func() bool { return s.ui.messageCount() == 3 })
	s.settled()
	if m := s.ui.said(); m[2] != "Slowly: yes." {
		t.Fatalf("messages after the slow answer = %q", m)
	}
	// Each answer went out after the turn that gave it.
	turns := s.events(event.RelayTurn)
	var msgs []event.Event
	for _, ev := range s.events(event.Message) {
		msgs = append(msgs, ev)
	}
	if len(turns) != 2 || len(msgs) != 3 || msgs[0].Seq < turns[0].Seq || msgs[2].Seq < turns[1].Seq || msgs[1].Seq > turns[1].Seq {
		t.Fatalf("order: turns %d,%d messages %v", turns[0].Seq, turns[len(turns)-1].Seq, msgs)
	}
}

// Old TestRolloverProducesDossierAndNewSubsession: when the conversation
// fills up the session continues in a new file that opens with a dossier;
// the log replays to two subsessions, the second a rollover carrying the
// dossier, and the session goes on answering.
func TestCoreRolloverLeavesARecordThatReplays(t *testing.T) {
	var big atomic.Bool
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			return reply{text: briefing(places(ask)[:3])}
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			r = say("Here.")
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
	s.settled()
	s.type_("@e still there?")
	s.waitFor("an answer after the rollover", func() bool { return s.ui.messageCount() == 2 })
	s.settled()
	path := s.rt.sess.Path
	s.stop()
	files, _ := store.ReadOnly(path).Files()
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	st := state.Replay(mustReadCore(t, path))
	if len(st.Subsessions) != 2 || st.Subsessions[1].Reason != "rollover" || st.Subsessions[1].File != files[1] || !strings.Contains(st.Subsessions[1].Dossier, "WHAT THE USER WANTS") {
		t.Fatalf("subsessions = %+v", st.Subsessions)
	}
	for _, ev := range mustReadCore(t, path) {
		if ev.Type == event.Dossier && ev.Source.File != files[1] {
			t.Fatalf("the dossier is in %s, want %s", ev.Source.File, files[1])
		}
	}
}

func mustReadCore(t *testing.T, path string) []event.Event {
	t.Helper()
	evs, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}
