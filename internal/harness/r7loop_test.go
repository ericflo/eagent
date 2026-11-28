package harness

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/store"
)

// Finding 21: a reply with no tool call and no words (a provider's error or
// refusal stop) is sent back once like any other reply that reaches no one,
// so the agent's report still reaches the user.
func TestR7AnEmptyReplyIsSentBackOnce(t *testing.T) {
	var mu sync.Mutex
	reminded := 0
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(tail, "had no tool call"):
			reminded++
			return say("Codex merged the PRs.")
		case strings.Contains(tail, "finished a turn"):
			return reply{}
		}
		return hold()
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	mu.Lock()
	defer mu.Unlock()
	if reminded != 1 {
		t.Fatalf("reminded %d times, want 1", reminded)
	}
}

// Finding 24: a closing word that is only a leaked control token says
// nothing, so it is not taken as said: send_message is refused, plain text
// is sent back as a reply with no tool call, and the session does not end
// in silence.
func TestR7AClosingWordOfOnlyControlTokensIsNotTakenAsSaid(t *testing.T) {
	const report = "Final: #42 and #44 are merged; #45 is not."
	t.Run("send_message", func(t *testing.T) {
		s, _, code := audit2Closing(t, func(msgs []map[string]any) reply {
			if strings.Contains(lastText(msgs), "text is required") {
				return say(report)
			}
			return say("<|im_end|>")
		})
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if said := s.ui.said(); len(said) == 0 || said[len(said)-1] != report {
			t.Fatalf("said %q", said)
		}
	})
	t.Run("plain text", func(t *testing.T) {
		s, _, code := audit2Closing(t, func(msgs []map[string]any) reply { return reply{text: "<|im_end|>"} })
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if n := audit2CountLast(s.model, "That reply had no tool call"); n != 1 {
			t.Fatalf("the empty report was sent back %d times, want 1", n)
		}
		// The earlier message was the last thing said before the closing
		// look; something must come after it.
		if said := s.ui.said(); len(said) < 2 {
			t.Fatalf("said %q", said)
		}
	})
}

// r7ResumeOld is resumeOld with the prompt given (none when "").
func r7ResumeOld(t *testing.T, project string, sess *store.Session, prompt string, brain func(model string, msgs []map[string]any) reply) *session {
	t.Helper()
	seat := newFakeSeat("Filled in the changelog.")
	useFakeRelays(t, seat)
	model := newScripted(brain)
	t.Cleanup(model.srv.Close)
	ui := &fakeUI{input: make(chan string, 4)}
	rt, err := Resume(testConfig(model.srv.URL), Options{Project: project, Interactive: true, Prompt: prompt}, ui, sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, rt: rt, ui: ui, seat: seat, model: model, done: make(chan int, 1)}
	ctx, cancel := contextWithCancel()
	s.cancel = cancel
	go func() { s.done <- rt.Run(ctx) }()
	t.Cleanup(s.stop)
	return s
}

// r7Looks counts the calls of a session's model that were not the dossier
// pass's.
func r7Looks(s *session) int {
	n := 0
	for _, q := range s.model.requests(testModel) {
		var msgs []map[string]any
		for _, raw := range q.Messages {
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			msgs = append(msgs, m)
		}
		if dossierRequest(msgs) == "" {
			n++
		}
	}
	return n
}

// Finding 2, an interrupted upgrade: a session of the earlier design is
// resumed with a message and stopped while its upgrade dossier is being
// written. The next run, with no message, finishes the upgrade and the
// message is still answered: it reaches an agent.
func TestR7AMessageOfAnInterruptedUpgradeIsAnsweredOnTheNextRun(t *testing.T) {
	project, sess, files := oldSession(t)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	asked := make(chan struct{}, 1)
	s := r7ResumeOld(t, project, sess, "fill in the changelog too", func(_ string, msgs []map[string]any) reply {
		if dossierRequest(msgs) != "" {
			select {
			case asked <- struct{}{}:
			default:
			}
			return reply{block: gate}
		}
		return hold()
	})
	select {
	case <-asked:
	case <-time.After(10 * time.Second):
		t.Fatal("the upgrade's dossier was never asked for")
	}
	s.stop()
	s = r7ResumeOld(t, project, sess, "", func(_ string, msgs []map[string]any) reply {
		if dossierRequest(msgs) != "" {
			return reply{text: briefing([]string{files[0] + ":3", files[1] + ":3"})}
		}
		if r, ok := startsAnAgent(msgs); ok {
			return r
		}
		return hold()
	})
	s.waitFor("the message to reach a new agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	if got := s.seat.sentTexts(); len(got) != 1 || got[0] != "fill in the changelog too" {
		t.Fatalf("the agent was sent %q", got)
	}
}

// Finding 2, a clean upgrade: the upgrade is written and nothing is owed.
// The next run has nothing to look at: the old design's messages were
// dealt with before the upgrade.
func TestR7ACleanUpgradeLeavesNothingOwedOnTheNextRun(t *testing.T) {
	project, sess, files := oldSession(t)
	brain := func(_ string, msgs []map[string]any) reply {
		if dossierRequest(msgs) != "" {
			return reply{text: briefing([]string{files[0] + ":3", files[1] + ":3"})}
		}
		return hold()
	}
	s := r7ResumeOld(t, project, sess, "", brain)
	r4Until(t, "the upgrade's dossier", func() bool { return len(s.dossiers()) == 2 })
	s.settled()
	s.stop()
	s = r7ResumeOld(t, project, sess, "", brain)
	s.settled()
	s.stop()
	if n := r7Looks(s); n != 0 {
		t.Fatalf("the run after a clean upgrade made %d looks at the old design's events", n)
	}
}
