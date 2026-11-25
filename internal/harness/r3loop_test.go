package harness

import (
	"strings"
	"sync/atomic"
	"testing"

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
