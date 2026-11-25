package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// Tests for what a read of the whole branch found in the loop: lines that
// moved between one prompt and the next, calls of a replaced conversation
// that came back after its dossier, and wakes a reply could step around.

// extendsEachOther fails unless every request is the one before it with
// messages added at the end, or that minus the end of its prompt: the
// lines written for one call ("[eagent 09:13:03] ..."), which are on the
// record only when the call acted on them.
func extendsEachOther(t *testing.T, reqs []request) {
	t.Helper()
	for i := 1; i < len(reqs); i++ {
		prev, q := reqs[i-1].Messages, reqs[i].Messages
		common := 0
		for common < len(prev) && common < len(q) && bytes.Equal(prev[common], q[common]) {
			common++
		}
		if common == len(prev) {
			continue
		}
		var last struct{ Role, Content string }
		_ = json.Unmarshal(prev[len(prev)-1], &last)
		if common < len(prev)-1 || last.Role != "user" || !strings.HasPrefix(last.Content, "[eagent ") {
			t.Fatalf("call %d shares %d of the previous call's %d messages:\nprev %s\nnow  %s", i, common, len(prev), prev[common], q[common])
		}
	}
}

// A turn that makes several calls keeps every line where the model first
// saw it. What a call's own tools put on the record (an agent starting or
// stopping, an autopilot order) is shown to the turn's next call before
// its reply, and stays before that reply in every later prompt.
func TestRequestPrefixIsStableWhenATurnMakesSeveralCalls(t *testing.T) {
	for _, c := range []struct{ name, args string }{
		{"relay_autopilot", `{"instruction":"keep merging the open PRs","until":"1h"}`},
		{"relay_start", `{"profile":"claude"}`},
		{"relay_control", `{"action":"stop","reason":"the user asked"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			acted := false
			s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
				mu.Lock()
				defer mu.Unlock()
				called := audit2Called(msgs)
				tail, all := lastUserText(msgs), allText(msgs)
				switch {
				case msgs[len(msgs)-1]["role"] != "tool" && strings.Contains(tail, "kept for you") && strings.Contains(all, "are you there") && !strings.Contains(all, "Still here."):
					return say("Still here.")
				case len(called) == 1 && called[0] == c.name && !strings.Contains(all, "Done: "):
					return say("Done: " + c.name + ".")
				case called == nil && strings.Contains(tail, "kept for you") && !acted:
					acted = true
					return reply{calls: []event.ToolCall{tc(c.name, c.args)}}
				}
				return hold()
			}, nil)
			useRelayFactory(t, "Ready.")
			s.type_("@e please do it")
			s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
			s.settled()
			s.type_("@e are you there")
			s.waitFor("the answer", func() bool { return s.ui.messageCount() == 2 })
			s.settled()
			reqs := s.model.requests(testModel)
			if len(reqs) < 3 {
				t.Fatalf("only %d model calls", len(reqs))
			}
			extendsEachOther(t, reqs)
			// And the record renders now exactly as the last call saw it.
			var now int
			s.read(func(*state.State) { now = len(s.rt.st.View(&s.rt.view)) })
			if last := reqs[len(reqs)-1].Messages; now < len(last)-2 {
				t.Fatalf("the record renders %d messages; the last call was given %d", now, len(last)-1)
			}
		})
	}
}

// A reply sent back for another try is answered from the conversation the
// first try was given: the second request is the first one plus the reply
// and the reason, whatever arrived in between.
func TestASentBackReplyIsAnsweredFromTheSameConversation(t *testing.T) {
	var s *session
	var once atomic.Bool
	ready := make(chan struct{})
	s = startSession(t, "", func(_ string, msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(lastText(msgs), "That reply had no tool call"):
			return say("Here.")
		case strings.Contains(tail, "kept for you") && once.CompareAndSwap(false, true):
			// Something arrives while the first try is being written.
			<-ready
			before := len(s.events(event.RelayProgress))
			s.seat.emit(relay.Event{Kind: relay.KindProgress, Text: "Rebasing the branch."})
			s.waitFor("the remark", func() bool { return len(s.events(event.RelayProgress)) > before })
			return reply{text: "I am here."}
		}
		return hold()
	}, nil)
	close(ready)
	s.type_("@e are you there")
	s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	reqs := s.model.requests(testModel)
	joined := func(msgs []json.RawMessage) string {
		var b strings.Builder
		for _, m := range msgs {
			b.Write(m)
			b.WriteByte('\n')
		}
		return b.String()
	}
	var first, second string
	for i, q := range reqs {
		if i > 0 && bytes.Contains(q.Messages[len(q.Messages)-1], []byte("That reply had no tool call")) {
			first, second = joined(reqs[i-1].Messages), joined(q.Messages[:len(q.Messages)-2])
		}
	}
	if first == "" {
		t.Fatal("the reply was not sent back")
	}
	if first != second {
		t.Fatalf("the second try was given another conversation:\nfirst  %s\nsecond %s", first, second)
	}
}

// eagent's own calls in the conversation a dossier replaced are not shown
// again after it. A message that arrived during the last of those calls
// is: it comes right after the briefing, and the old call does not.
func TestOwnCallsOfTheReplacedConversationStayBehindTheDossier(t *testing.T) {
	const marker, old = "PLEASE-ALSO-TAG-THE-RELEASE-5512", "On it: merging the open PRs first."
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var first atomic.Bool
	var mu sync.Mutex
	var after []map[string]any
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			return reply{text: briefing(places(ask)[:3])}
		}
		if len(msgs) > 1 {
			if open, _ := msgs[1]["content"].(string); strings.HasPrefix(open, "[Briefing.") {
				mu.Lock()
				if after == nil {
					after = msgs
				}
				mu.Unlock()
			}
		}
		if first.CompareAndSwap(false, true) {
			// A call that acts, and whose prompt is past the limit. The
			// user writes while it is in flight.
			r := say(old)
			r.prompt, r.block = 25000, release
			return r
		}
		return hold()
	}, nil)
	s.waitFor("the first call", func() bool { return s.model.count(testModel) == 1 })
	s.type_("@e " + marker)
	s.waitFor("the message", func() bool {
		for _, ev := range s.events(event.UserMessage) {
			if strings.Contains(string(ev.Data), marker) {
				return true
			}
		}
		return false
	})
	close(release)
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	s.waitFor("a call after the restart", func() bool { mu.Lock(); defer mu.Unlock(); return after != nil })
	var msgSeq, callSeq int64
	for _, ev := range s.events(event.UserMessage) {
		if strings.Contains(string(ev.Data), marker) {
			msgSeq = ev.Seq
		}
	}
	for _, ev := range s.events(event.Assistant) {
		if callSeq == 0 {
			callSeq = ev.Seq
		}
	}
	if d := s.dossiers()[0]; d.CoversSeq >= msgSeq || callSeq < msgSeq {
		t.Fatalf("not the case under test: dossier covers %d, message %d, first call recorded at %d", d.CoversSeq, msgSeq, callSeq)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(allText(after), marker) {
		t.Fatal("the message that arrived during the call is not shown after the briefing")
	}
	for i, m := range after[1:] {
		if m["role"] != "user" {
			t.Fatalf("message %d after the briefing is a %v message of the replaced conversation: %v", i+1, m["role"], m)
		}
	}
	if strings.Contains(allText(after[2:]), old) || strings.Contains(allText(after[2:]), "kept for you") && strings.Count(allText(after[2:]), "kept for you") > 1 {
		t.Fatalf("the replaced conversation's own turn is shown again: %s", allText(after[2:]))
	}
}

// A reply that sets the title and does nothing else has not dealt with
// what the wake was for. It is told so once, and the turn goes on.
func TestAThreadUpdateAloneDoesNotSettleAWake(t *testing.T) {
	const update = `{"summary":"Merging the pending PRs on main."}`
	t.Run("the user's answer", func(t *testing.T) {
		var mu sync.Mutex
		var reminded, updated int
		var afterTool bool
		s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
			mu.Lock()
			defer mu.Unlock()
			tail := lastUserText(msgs)
			switch {
			case strings.Contains(lastText(msgs), "The user's answer has reached nobody but you"):
				reminded++
				afterTool = msgs[len(msgs)-2]["role"] == "tool"
				return reply{calls: []event.ToolCall{tc("relay_send", `{"text":"The user says yes: merge #45 as well."}`)}}
			case msgs[len(msgs)-1]["role"] == "tool":
				return hold()
			case strings.Contains(tail, "The user answered your question"):
				updated++
				return reply{calls: []event.ToolCall{tc("update_thread", update)}}
			case strings.Contains(tail, "kept for you"):
				return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge #45 as well?","options":["yes","no"]}`)}}
			}
			return hold()
		}, nil)
		s.type_("@e ask me whether to merge #45")
		s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
		s.settled()
		sent := len(s.seat.sentTexts())
		s.type_("yes")
		s.waitFor("the answer to reach the agent", func() bool { return len(s.seat.sentTexts()) > sent })
		s.settled()
		mu.Lock()
		defer mu.Unlock()
		if updated != 1 || reminded != 1 || !afterTool {
			t.Fatalf("thread updates %d, reminders %d (after the update's result: %v); want one of each", updated, reminded, afterTool)
		}
		if got := s.seat.sentTexts(); !strings.Contains(got[len(got)-1], "merge #45") {
			t.Fatalf("the agent was sent %q", got)
		}
	})
	t.Run("a message kept for eagent", func(t *testing.T) {
		var mu sync.Mutex
		var reminded int
		s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
			mu.Lock()
			defer mu.Unlock()
			tail := lastUserText(msgs)
			switch {
			case strings.Contains(lastText(msgs), "What you just did is not an answer to it"):
				reminded++
				return say("Still here.")
			case msgs[len(msgs)-1]["role"] == "tool":
				return hold()
			case strings.Contains(tail, "kept for you"):
				return reply{calls: []event.ToolCall{tc("update_thread", update)}}
			}
			return hold()
		}, nil)
		s.type_("@e are you there")
		s.waitFor("the answer", func() bool { return s.ui.messageCount() == 1 })
		s.settled()
		mu.Lock()
		defer mu.Unlock()
		if reminded != 1 {
			t.Fatalf("reminded %d times, want 1", reminded)
		}
	})
	t.Run("the closing word", func(t *testing.T) {
		var mu sync.Mutex
		var reminded int
		_, ui, _ := batch(t, brief+" Stop when done.", "Done: #42 and #44 are merged.", func(_ string, msgs []map[string]any) reply {
			mu.Lock()
			defer mu.Unlock()
			tail := lastUserText(msgs)
			switch {
			case strings.Contains(lastText(msgs), "the user is owed a last word, which they have not had"):
				reminded++
				return say("All done: #42 and #44 are merged on main.")
			case msgs[len(msgs)-1]["role"] == "tool":
				return hold()
			case strings.Contains(tail, "The session is ending now"):
				return reply{calls: []event.ToolCall{tc("update_thread", update)}}
			case strings.Contains(tail, "finished a turn"):
				return stopsWhenDone()
			}
			return hold()
		})
		mu.Lock()
		defer mu.Unlock()
		if said := ui.said(); reminded != 1 || len(said) != 1 || !strings.HasPrefix(said[0], "All done:") {
			t.Fatalf("reminded %d times, said %q", reminded, said)
		}
	})
}

// A question is eagent speaking: the session's memory of when the user
// last heard from it is the same in a running session as in one read back
// from its log.
func TestAQuestionCountsAsHavingSpoken(t *testing.T) {
	s := startSession(t, "", func(_ string, msgs []map[string]any) reply {
		if msgs[len(msgs)-1]["role"] != "tool" && strings.Contains(lastUserText(msgs), "kept for you") {
			return reply{calls: []event.ToolCall{tc("ask_user", `{"text":"Merge #45 as well?"}`)}}
		}
		return hold()
	}, nil)
	s.type_("@e ask me whether to merge #45")
	s.waitFor("the question", func() bool { return s.ui.askedCount() == 1 })
	s.settled()
	var live, replayed int64
	s.read(func(st *state.State) {
		live = s.rt.turns.saidSeq
		var t2 turnState
		t2.init(st)
		replayed = t2.saidSeq
	})
	if q := s.events(event.Question); len(q) != 1 || live != q[0].Seq || replayed != live {
		t.Fatalf("saidSeq is %d live and %d from the log; the question is event %v", live, replayed, q)
	}
}

const tooLongBody = `{"error":{"message":"prompt is too long: 250000 tokens > 200000 maximum","type":"invalid_request_error"}}`

// dossierAfterALook starts a session whose first look reports a prompt
// past the limit, so the dossier pass begins at once. write answers every
// call of the pass.
func dossierAfterALook(t *testing.T, tune func(*config.Config), write func(ask string, msgs []map[string]any) reply) *session {
	t.Helper()
	var first atomic.Bool
	return startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if ask := dossierRequest(msgs); ask != "" {
			return write(ask, msgs)
		}
		r := hold()
		if first.CompareAndSwap(false, true) {
			r.prompt = 25000
		}
		return r
	}, tune)
}

const fromTheFiles = "You no longer have the conversation in front of you"

// A conversation that nearly fills the model's window leaves no room to
// read the record on top of it. The dossier is then written from the
// files, where the whole window is free, and not inside the conversation,
// where the first lookups would be refused as too long.
func TestDossierIsWrittenFromTheFilesWhenTheConversationFillsTheWindow(t *testing.T) {
	var mu sync.Mutex
	var asks []string
	s := dossierAfterALook(t, func(cfg *config.Config) {
		// 25,000 tokens of conversation and 30,000 kept for the reply, in
		// a window of 60,000.
		cfg.Model.ContextTokens, cfg.Model.MaxTokens = 60_000, 30_000
	}, func(ask string, msgs []map[string]any) reply {
		mu.Lock()
		asks = append(asks, ask)
		mu.Unlock()
		return reply{text: briefing(places(ask)[:3])}
	})
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	mu.Lock()
	defer mu.Unlock()
	if len(asks) != 1 || !strings.Contains(asks[0], fromTheFiles) || !strings.Contains(asks[0], "fills too much of the model's context window") {
		t.Fatalf("the pass was asked %d times; first:\n%s", len(asks), asks[0])
	}
	if d := s.dossiers()[0]; d.By != "eagent" {
		t.Fatalf("dossier by %q", d.By)
	}
}

// A dossier pass the provider refuses as too long is not asked the same
// thing again. Lookups that did not fit are taken out and the model
// writes without them; a conversation that does not fit is left and the
// model writes from the files. Either way the model's dossier is kept,
// where the harness would otherwise have had to write one itself.
func TestDossierPassRefusedAsTooLongIsMadeSmaller(t *testing.T) {
	t.Run("the lookups did not fit", func(t *testing.T) {
		var mu sync.Mutex
		refused := 0
		var wrote []map[string]any
		s := dossierAfterALook(t, nil, func(ask string, msgs []map[string]any) reply {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case strings.Contains(lastText(msgs), "did not fit in the model's context window"):
				wrote = msgs
				return reply{text: briefing(places(ask)[:3])}
			case msgs[len(msgs)-1]["role"] == "tool":
				refused++
				return reply{status: 400, body: tooLongBody}
			}
			return reply{calls: []event.ToolCall{tc("session_read", fmt.Sprintf(`{"file":%q}`, strings.SplitN(places(ask)[0], ":", 2)[0]))}}
		})
		s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
		mu.Lock()
		defer mu.Unlock()
		if d := s.dossiers()[0]; d.By != "eagent" || refused != 1 {
			t.Fatalf("dossier by %q after %d refusals", d.By, refused)
		}
		for _, m := range wrote {
			if m["role"] == "tool" {
				t.Fatal("the lookups that did not fit are still in the request")
			}
		}
		if strings.Contains(allText(wrote), fromTheFiles) {
			t.Fatal("the conversation was given up, though it fitted before the lookups")
		}
	})
	t.Run("the conversation did not fit", func(t *testing.T) {
		var mu sync.Mutex
		refused := 0
		var fresh []map[string]any
		s := dossierAfterALook(t, nil, func(ask string, msgs []map[string]any) reply {
			mu.Lock()
			defer mu.Unlock()
			if !strings.Contains(ask, fromTheFiles) {
				refused++
				return reply{status: 400, body: tooLongBody}
			}
			fresh = msgs
			return reply{text: briefing(places(ask)[:3])}
		})
		s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
		mu.Lock()
		defer mu.Unlock()
		if d := s.dossiers()[0]; d.By != "eagent" || refused != 1 {
			t.Fatalf("dossier by %q after %d refusals", d.By, refused)
		}
		if len(fresh) != 2 || !strings.Contains(lastUserText(fresh), "refused the conversation as too long") {
			t.Fatalf("the writer was given %d messages; the last:\n%s", len(fresh), lastUserText(fresh))
		}
	})
}

// The warning that a restart cannot make room is given once for a
// conversation, not once for a session: a later conversation that is too
// long from its first call is reported again.
func TestFullConversationWarningIsPerConversation(t *testing.T) {
	s := dossierAfterALook(t, nil, func(ask string, msgs []map[string]any) reply {
		return reply{text: briefing(places(ask)[:3])}
	})
	s.rt.sync(func() { s.rt.turns.futileLogged = true })
	s.waitFor("the dossier", func() bool { return len(s.dossiers()) == 1 })
	var logged bool
	s.rt.sync(func() { logged = s.rt.turns.futileLogged })
	if logged {
		t.Fatal("the new conversation still counts as warned about")
	}
}
