package harness

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
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
