package harness

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// refusedSends returns the results of send_message calls that were refused
// as a second message.
func (s *session) refusedSends() []string {
	var out []string
	for _, ev := range s.events(event.ToolResult) {
		var d event.ToolResultData
		if ev.Decode(&d) == nil && d.Name == "send_message" && strings.Contains(d.Output, "one message per reply") {
			out = append(out, d.Output)
		}
	}
	return out
}

// "One message per reply" is about one reply. A turn that says what it is
// about to do, looks, and then says what it found has made two replies, and
// both messages go out.
func TestATurnMaySpeakAgainInALaterReply(t *testing.T) {
	var asked atomic.Bool
	brain := func(_ string, msgs []map[string]any) reply {
		switch {
		case msgs[len(msgs)-1]["role"] == "tool" && asked.Load():
			return say("Codex is idle on a clean tree.")
		case strings.Contains(lastUserText(msgs), "kept for you"):
			asked.Store(true)
			return reply{calls: []event.ToolCall{
				tc("send_message", `{"text":"Looking at Codex now."}`),
				tc("relay_screen", `{}`),
			}}
		}
		return hold()
	}
	s := startSession(t, "", brain, nil)
	s.settled()
	said := s.ui.messageCount()
	s.type_("@e what is Codex doing?")
	s.waitFor("the first message", func() bool { return s.ui.messageCount() > said })
	s.settled()
	if refused := s.refusedSends(); len(refused) > 0 {
		t.Fatalf("the message of a later reply was refused: %q", refused[0])
	}
	if got := s.ui.messageCount() - said; got != 2 {
		t.Fatalf("the user was told %d things, want the two the turn said: %q", got, s.ui.said())
	}
}

// One reply still sends one message: the second send_message of the same
// reply is refused, as before.
func TestOneReplyStillSendsOneMessage(t *testing.T) {
	brain := func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			return reply{calls: []event.ToolCall{
				tc("send_message", `{"text":"First half."}`),
				tc("send_message", `{"text":"Second half."}`),
			}}
		}
		return hold()
	}
	s := startSession(t, "", brain, nil)
	s.settled()
	said := s.ui.messageCount()
	s.type_("@e what is Codex doing?")
	s.waitFor("the message", func() bool { return s.ui.messageCount() > said })
	s.settled()
	if got := s.ui.messageCount() - said; got != 1 {
		t.Fatalf("one reply sent %d messages, want 1", got)
	}
	if len(s.refusedSends()) != 1 {
		t.Fatalf("the second message of one reply was not refused")
	}
}

// A message that arrives for eagent while a turn is running is answered in
// that turn, even when the turn has already said something about the
// message before it.
func TestAMessageArrivingMidTurnIsAnsweredInThatTurn(t *testing.T) {
	old := owedRetryDelays
	defer func() { owedRetryDelays = old }()
	gate := make(chan struct{})
	var calls atomic.Int32
	brain := func(_ string, msgs []map[string]any) reply {
		if !strings.Contains(allText(msgs), "what is Codex doing") {
			return hold()
		}
		switch calls.Add(1) {
		case 1:
			// The second message arrives while this call is out.
			return reply{block: gate, calls: []event.ToolCall{
				tc("send_message", `{"text":"Looking at Codex now."}`),
				tc("relay_screen", `{}`),
			}}
		case 2:
			return say("It is on main, and yes, the tests passed.")
		}
		return hold()
	}
	s := startSession(t, "", brain, nil)
	s.settled()
	said := s.ui.messageCount()
	s.type_("@e what is Codex doing?")
	s.waitFor("the first call", func() bool { return calls.Load() == 1 })
	s.type_("@e and did the tests pass?")
	s.waitFor("the second message on the record", func() bool {
		n := 0
		for _, ev := range s.events(event.UserMessage) {
			var d event.UserMessageData
			if ev.Decode(&d) == nil && strings.Contains(d.Text, "did the tests pass") {
				n++
			}
		}
		return n == 1
	})
	close(gate)
	s.waitFor("both answers", func() bool { return s.ui.messageCount()-said >= 2 || len(s.refusedSends()) > 0 })
	s.settled()
	if refused := s.refusedSends(); len(refused) > 0 {
		t.Fatalf("the answer to the second message was refused: %q", refused[0])
	}
	var owed int64
	s.rt.sync(func() { owed = s.rt.turns.owed })
	if owed != 0 {
		t.Fatal("the second message is still owed an answer after the turn answered it")
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("the two messages took %d calls, want 2", n)
	}
}

// phoneShows waits up to within for the phone's status line to read text.
func phoneShows(fp *phoneFake, text string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		for _, a := range fp.statuses() {
			if a["text"] == text {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// While a model call is out the phone says eagent is thinking. Starting a
// call records nothing, so the status has to follow the runtime's state and
// not only the record.
func TestPhoneSaysEagentIsThinkingWhileACallIsOut(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var out atomic.Bool
	brain := func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "kept for you") {
			out.Store(true)
			return reply{block: gate, calls: say("Codex is idle.").calls}
		}
		return hold()
	}
	g := phoneStart(t, fp, "", true, brain, nil)
	t.Cleanup(release) // before the model server is closed
	waitFor(t, "the first status", func() bool { return len(fp.statuses()) > 0 })
	g.ui.input <- "@e what is Codex doing?"
	waitFor(t, "the call to go out", out.Load)
	shown := phoneShows(fp, "Thinking about the next step", 8*time.Second)
	release()
	if !shown {
		t.Fatalf("the phone never said eagent was thinking while its call was out; it showed %v", fp.statuses())
	}
	// And the line goes back when the call is over.
	waitFor(t, "the answer", func() bool { return g.ui.messageCount() > 0 })
	waitFor(t, "the status to leave thinking", func() bool {
		st := fp.statuses()
		return len(st) > 0 && st[len(st)-1]["text"] != "Thinking about the next step"
	})
}

// A dossier pass takes minutes and eagent says nothing else meanwhile. The
// phone says the session is being reread, also when the pass starts
// without recording anything first.
func TestPhoneSaysTheSessionIsBeingRereadDuringADossier(t *testing.T) {
	fp := newPhoneFake(false)
	t.Cleanup(fp.srv.Close)
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var prompt atomic.Int64
	var writing atomic.Bool
	brain := func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			writing.Store(true)
			p := places(ask)
			return reply{block: gate, text: briefing(p[:min(len(p), 4)])}
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			r = say("Here.")
		}
		if p := prompt.Swap(0); p > 0 {
			r.prompt = int(p)
		}
		return r
	}
	g := phoneStart(t, fp, brief, true, brain, nil)
	t.Cleanup(release)
	waitFor(t, "the agent's turn", func() bool { return len(g.events(event.RelayTurn)) == 1 })
	waitFor(t, "the first status", func() bool { return len(fp.statuses()) > 0 })
	prompt.Store(21_000) // over the test's rollover size: the next turn starts a dossier
	g.ui.input <- "@e hello"
	waitFor(t, "the dossier call to go out", writing.Load)
	shown := phoneShows(fp, "Rereading the session so far, to carry on with a clear head", 8*time.Second)
	release()
	if !shown {
		t.Fatalf("the phone never said the session was being reread; it showed %v", fp.statuses())
	}
}

// A session with no terminal and no phone cannot be answered while it
// runs. eagent is told that asking ends such a session until the user
// resumes it, and so it does, with exit 2, while the agent goes on running
// in its own terminal for the resumed session to pick up.
func TestAQuestionNobodyCanAnswerEndsTheSessionAndLeavesTheAgent(t *testing.T) {
	rt, ui, seat := batchExit(t, 2, brief, "Both are ready. Merge to main, or open a pull request?", func(_ string, msgs []map[string]any) reply {
		if strings.Contains(lastUserText(msgs), "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" {
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge to main, or open a pull request?"}`)}}
		}
		return hold()
	})
	if ui.askedCount() != 1 {
		t.Fatalf("the question was shown %d times", ui.askedCount())
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	st := state.Replay(evs)
	if st.EndReason != "awaiting-input" || st.Question == nil {
		t.Fatalf("end reason %q, question %v; want awaiting-input with the question open", st.EndReason, st.Question)
	}
	if seat.wasKilled() || len(st.ActiveRelays()) != 1 {
		t.Fatalf("the agent was stopped (killed %v, %d active): it has to be there for the resumed session", seat.wasKilled(), len(st.ActiveRelays()))
	}
}

// A reply that is sent back for another try is a call of the conversation
// like any other. When it is the first call after a restart, its prompt is
// the conversation's first prompt: the guard against restarting a
// conversation that was too long from the start must go by it, and not by
// the next call's, which carries the exchange on top. And what the running
// session counts is what a resumed one would read back from the record.
func TestASentBackFirstCallIsTheConversationsFirstCall(t *testing.T) {
	var prompt atomic.Int64
	var stage, passes atomic.Int32
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	brain := func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			p := places(ask)
			r := reply{text: briefing(p[:min(len(p), 4)])}
			if passes.Add(1) == 2 {
				r.block = gate // held, so the test can look at the conversation it closes
			}
			return r
		}
		switch stage.Load() {
		case 1:
			// The first call of the restarted conversation: under the
			// limit, and plain text, so it is sent back.
			stage.Store(2)
			return reply{text: "Codex is idle.", prompt: 19_000}
		case 2:
			// The second carries the exchange and is over the limit.
			stage.Store(3)
			r := say("Codex is idle.")
			r.prompt = 21_000
			return r
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			r = say("Here.")
		}
		if p := prompt.Swap(0); p > 0 {
			r.prompt = int(p)
		}
		return r
	}
	s := startSession(t, brief, brain, nil)
	t.Cleanup(release)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	prompt.Store(21_000)
	s.type_("@e hello")
	s.waitFor("the first dossier", func() bool { return len(s.dossiers()) == 1 })
	s.settled()

	stage.Store(1)
	said := s.ui.messageCount()
	s.type_("@e what is Codex doing?")
	s.waitFor("the answer in the new conversation", func() bool { return s.ui.messageCount() > said })
	// The conversation grew past the limit, so the next thing to weigh
	// restarts it again. The user is not told that a restart cannot help:
	// it started under the limit.
	s.type_("@e and now?")
	gaveUp := func() bool {
		for _, m := range s.ui.said() {
			if strings.HasPrefix(m, fullNotice) {
				return true
			}
		}
		return false
	}
	s.waitFor("the second dossier pass", func() bool { return passes.Load() == 2 || gaveUp() })
	if gaveUp() {
		t.Fatal("the user was told a restart cannot help, in a conversation that started under the limit")
	}
	// While the pass is held: what the running session counts for the
	// conversation, and what a resumed one would read back.
	var calls, first int
	s.rt.sync(func() { calls, first = s.rt.turns.subCalls, s.rt.turns.firstPrompt })
	if first != 19_000 || calls < 2 {
		t.Fatalf("the conversation counts %d calls and a first prompt of %d, want at least 2 and 19000", calls, first)
	}
	evs, err := store.Read(s.rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	var again turnState
	again.init(state.Replay(evs))
	if again.subCalls != calls || again.firstPrompt != first {
		t.Fatalf("a resumed session would count %d calls and a first prompt of %d; the running one has %d and %d", again.subCalls, again.firstPrompt, calls, first)
	}
	release()
	s.waitFor("the second dossier", func() bool { return len(s.dossiers()) == 2 })
}

// A session of the earlier design is picked up from a dossier written from
// its record. When the conversation that dossier opens is already at the
// limit on its first call, writing another dossier would rebuild the same
// prompt, as after any restart: the user is told once and no second pass
// is paid for.
func TestAConversationTooLongRightAfterBeingPickedUpIsNotRestartedAgain(t *testing.T) {
	project, sess, files := oldSession(t)
	var passes atomic.Int32
	s, _ := resumeOld(t, project, sess, func(_ string, msgs []map[string]any) reply {
		if dossierRequest(msgs) != "" {
			passes.Add(1)
			return reply{text: briefing([]string{files[0] + ":3", files[0] + ":5", files[1] + ":2", files[1] + ":3", files[1] + ":5"})}
		}
		// Every call of the new conversation is over the limit, the first
		// one included.
		r, ok := startsAnAgent(msgs)
		if !ok {
			r = hold()
		}
		r.prompt = 21_000
		return r
	})
	s.waitFor("the message to reach a new agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	if passes.Load() != 1 {
		t.Fatalf("picking the session up took %d dossier passes, want 1", passes.Load())
	}
	told := func() bool {
		for _, m := range s.ui.said() {
			if strings.HasPrefix(m, fullNotice) {
				return true
			}
		}
		return false
	}
	s.type_("@e where are we?")
	s.waitFor("the user to be told, or another pass", func() bool { return told() || passes.Load() > 1 })
	s.settled()
	if n := passes.Load(); n != 1 {
		t.Fatalf("a conversation that was too long from its first call was restarted again (%d passes)", n)
	}
	if !told() {
		t.Fatal("the user was not told the conversation cannot be made shorter")
	}
}

// When the file for the next conversation cannot be created, the dossier
// is given up for now and the session carries on in the conversation it
// has: the record still takes events, and nothing in it says the
// conversation ended.
func TestSessionCarriesOnWhenTheNextConversationFileCannotBeCreated(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made read-only for root")
	}
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var prompt atomic.Int64
	var writing atomic.Bool
	brain := func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			writing.Store(true)
			p := places(ask)
			return reply{block: gate, text: briefing(p[:min(len(p), 4)])}
		}
		r := hold()
		if strings.Contains(lastUserText(msgs), "kept for you") && msgs[len(msgs)-1]["role"] != "tool" {
			r = say("Here.")
		}
		if p := prompt.Swap(0); p > 0 {
			r.prompt = int(p)
		}
		return r
	}
	s := startSession(t, brief, brain, nil)
	t.Cleanup(release)
	s.waitFor("the agent's turn", func() bool { return len(s.events(event.RelayTurn)) == 1 })
	s.settled()
	prompt.Store(21_000)
	s.type_("@e hello")
	s.waitFor("the dossier call to go out", writing.Load)
	dir := s.rt.sess.Path
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	release()
	s.waitFor("the dossier to be given up", func() bool {
		for _, l := range s.ui.logged() {
			if strings.Contains(l, "the conversation continues as it is for now") {
				return true
			}
		}
		return false
	})
	said := s.ui.messageCount()
	s.type_("@e are you still there?")
	s.waitFor("an answer in the same conversation", func() bool {
		for _, l := range s.ui.logged() {
			if strings.Contains(l, "FATAL") {
				return true
			}
		}
		return s.ui.messageCount() > said
	})
	for _, l := range s.ui.logged() {
		if strings.Contains(l, "FATAL") {
			t.Fatalf("the session lost its record: %s", l)
		}
	}
	if n := len(s.events(event.SubsessionEnd)); n != 0 {
		t.Fatalf("the record says %d conversations ended, though no new one could be opened", n)
	}
	if n := len(s.dossiers()); n != 0 {
		t.Fatalf("%d dossiers recorded with no file to open", n)
	}
}
