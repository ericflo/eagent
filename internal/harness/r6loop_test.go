package harness

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// forwardRig is a session whose model, at the tail it is waiting for,
// replies with reply; when gate is set, that reply waits on it. Every tail
// the model is given is kept.
type forwardRig struct {
	s     *session
	mu    sync.Mutex
	tails []string
}

func (f *forwardRig) seen(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tails {
		if strings.Contains(t, sub) {
			return true
		}
	}
	return false
}

func userSeqs(s *session) []int64 {
	var out []int64
	for _, ev := range s.events(event.UserMessage) {
		out = append(out, ev.Seq)
	}
	return out
}

// sentContaining counts what the agent was typed that contains sub.
func sentContaining(s *session, sub string) int {
	n := 0
	for _, t := range s.seat.sentTexts() {
		if strings.Contains(t, sub) {
			n++
		}
	}
	return n
}

// The model's call saw "forward this checklist"; while it was out the user
// wrote eagent something private. relay_forward with no seq forwards the
// message the call was shown, never the one that came after it, and the
// private message gets a turn of its own and an answer.
func TestAForwardWithNoSeqSendsTheMessageTheCallSawNotANewerOne(t *testing.T) {
	gate := make(chan struct{})
	called := make(chan struct{}, 1)
	f := &forwardRig{}
	f.s = startSession(t, "", func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		afterTool := msgs[len(msgs)-1]["role"] == "tool"
		f.mu.Lock()
		if !afterTool {
			f.tails = append(f.tails, fmt.Sprintf("%v %s", strings.Contains(allText(msgs), "why are there two branches"), tail))
		} else {
			f.tails = append(f.tails, fmt.Sprintf("TOOL %v", msgs[len(msgs)-1]["content"]))
		}
		f.mu.Unlock()
		switch {
		case afterTool:
			return hold()
		case strings.Contains(allText(msgs), "why are there two branches") && strings.Contains(tail, "kept for you"):
			return say("There are two because the release branch was cut early.")
		case strings.Contains(tail, "kept for you"):
			select {
			case called <- struct{}{}:
			default:
			}
			return reply{calls: []event.ToolCall{tc("relay_forward", `{}`)}, block: gate}
		}
		return hold()
	}, nil)
	s := f.s
	s.type_("@e forward this checklist to Codex: lint, test, tag")
	<-called
	s.type_("@e between you and me, why are there two branches?")
	s.waitFor("the second message", func() bool { return len(userSeqs(s)) == 2 })
	close(gate)
	s.waitFor("the private message to be answered", func() bool { return s.ui.messageCount() > 0 })
	s.settled()
	if s.ui.messageCount() != 1 {
		f.mu.Lock()
		t.Fatalf("the private message was not answered; said %q; sent %q; tails:\n%s\nlogs %q", s.ui.said(), s.seat.sentTexts(), strings.Join(f.tails, "\n---\n"), s.ui.logged())
	}
	s.settled()
	if n := sentContaining(s, "between you and me"); n != 0 {
		t.Fatalf("the private message was typed into the agent: %q", s.seat.sentTexts())
	}
	if n := sentContaining(s, "checklist"); n != 1 {
		t.Fatalf("the checklist was typed %d times: %q", n, s.seat.sentTexts())
	}
}

// The cases beside it.
func TestAForwardResolvesItsMessageAgainstWhatItsCallWasShown(t *testing.T) {
	// The newer message is already on the record when the call is made:
	// the call was shown it, and with no seq it is the one forwarded (the
	// user's latest, as the tool says).
	t.Run("both shown", func(t *testing.T) {
		s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
			if msgs[len(msgs)-1]["role"] != "tool" && strings.Contains(lastUserText(msgs), "kept for you") {
				return reply{calls: []event.ToolCall{tc("relay_forward", `{}`)}}
			}
			return hold()
		}, nil)
		s.settled()
		s.rt.sync(func() {
			s.rt.onInput("@e forward this checklist to Codex: lint, test, tag")
			s.rt.onInput("@e and this one: deploy to staging")
		})
		s.waitFor("a forward", func() bool { return len(s.seat.sentTexts()) == 1 })
		s.settled()
		if got := s.seat.sentTexts(); !strings.Contains(got[0], "deploy to staging") {
			t.Fatalf("forwarded %q, want the latest message the call was shown", got)
		}
	})
	// A later call of the same turn is shown the newer message: it is that
	// call's default.
	t.Run("a later call shows it", func(t *testing.T) {
		gate := make(chan struct{})
		called := make(chan struct{}, 1)
		var mu sync.Mutex
		phase := 0
		s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
			mu.Lock()
			defer mu.Unlock()
			tail := lastUserText(msgs)
			switch {
			case phase == 0 && strings.Contains(tail, "kept for you"):
				phase = 1
				called <- struct{}{}
				return reply{calls: []event.ToolCall{tc("session_search", `{"query":"checklist"}`)}, block: gate}
			case phase == 1:
				phase = 2
				return reply{calls: []event.ToolCall{tc("relay_forward", `{}`)}}
			}
			return hold()
		}, nil)
		s.type_("@e forward this checklist to Codex: lint, test, tag")
		<-called
		s.type_("@e no, forward this instead: deploy to staging")
		s.waitFor("the second message", func() bool { return len(userSeqs(s)) == 2 })
		close(gate)
		s.waitFor("a forward", func() bool { return len(s.seat.sentTexts()) == 1 })
		s.settled()
		if got := s.seat.sentTexts(); !strings.Contains(got[0], "deploy to staging") {
			t.Fatalf("forwarded %q, want the message the second call was shown", got)
		}
	})
	// An explicit seq: an older message the call saw is allowed while a
	// newer one exists; one that came after the call is refused, and the
	// model is told it will be shown next; one that is not the user's is
	// refused as before.
	t.Run("explicit seq", func(t *testing.T) {
		// The model's reply waits for its arguments, which the test gives
		// once it knows the seqs; the next call reads the tool's result.
		argsCh := make(chan string)
		called := make(chan struct{}, 1)
		done := make(chan struct{})
		defer close(done)
		s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
			if msgs[len(msgs)-1]["role"] != "tool" && strings.Contains(lastUserText(msgs), "kept for you") {
				select {
				case called <- struct{}{}:
				case <-done:
					return hold()
				}
				select {
				case a := <-argsCh:
					return reply{calls: []event.ToolCall{tc("relay_forward", a)}}
				case <-done:
				}
			}
			return hold()
		}, nil)
		forward := func(args string) string {
			before := len(s.events(event.ToolResult))
			argsCh <- args
			var out string
			s.waitFor("the forward's result", func() bool {
				evs := s.events(event.ToolResult)
				if len(evs) == before {
					return false
				}
				var d event.ToolResultData
				_ = evs[len(evs)-1].Decode(&d)
				out = d.Output
				return true
			})
			return out
		}
		// An older message the call saw, while a newer one it also saw
		// exists: allowed.
		s.settled()
		s.rt.sync(func() {
			s.rt.onInput("@e forward this checklist to Codex: lint, test, tag")
			s.rt.onInput("@e and tell me when it is done")
		})
		<-called
		seqs := userSeqs(s)
		if out := forward(fmt.Sprintf(`{"seq":%d}`, seqs[0])); !strings.Contains(out, "transcript will confirm") {
			t.Fatalf("forwarding the older message the call saw: %q", out)
		}
		s.waitFor("the forward", func() bool { return sentContaining(s, "checklist") == 1 })
		s.settled()
		// A message that came in after the call was made: refused, and
		// the model is told it will see it next.
		s.type_("@e forward this too: deploy to staging")
		<-called
		s.type_("@e between you and me, why are there two branches?")
		s.waitFor("the newer message", func() bool { return len(userSeqs(s)) == 4 })
		seqs = userSeqs(s)
		out := forward(fmt.Sprintf(`{"seq":%d}`, seqs[3]))
		if !strings.Contains(out, "after you") || !strings.Contains(out, "shown to you next") {
			t.Fatalf("forwarding a message the call was not shown: %q", out)
		}
		// The call after the refusal is shown the newer message; it holds.
		// Then a seq that is not a user message.
		<-called
		if out := forward(`{"seq":1}`); !strings.Contains(out, "no user message with seq 1") {
			t.Fatalf("forwarding a seq that is not the user's: %q", out)
		}
		if n := sentContaining(s, "between you and me"); n != 0 {
			t.Fatalf("the private message was typed into the agent: %q", s.seat.sentTexts())
		}
	})
}
