package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// A generic "stop at every write" check. A small scripted session runs to
// its end; its record is then cut after every event, each prefix is put in
// a fresh project and resumed against the same scripted model, and the
// resumed session is let settle and stopped. Every cut must leave no user
// message passed over, none answered twice because of the cut, and a
// record that reads back with strictly increasing seqs.

// cutBrain replies once to each user message it has not answered: a
// reply names the user's text, so a reply after a tool result is not
// asked for again.
func cutBrain(_ string, msgs []map[string]any) reply {
	if msgs[len(msgs)-1]["role"] == "tool" {
		return hold()
	}
	last := len(msgs) - 1
	for last > 0 && msgs[last-1]["role"] == "user" {
		last--
	}
	if u := allText(msgs[last:]); strings.Contains(u, "cutq") {
		return say("noted")
	}
	return hold()
}

// cutSends reports whether an assistant event sends the user something.
func cutSends(ev event.Event) (event.AssistantData, bool) {
	var d event.AssistantData
	if ev.Type != event.Assistant || ev.Decode(&d) != nil {
		return d, false
	}
	for _, c := range d.ToolCalls {
		if c.Name == "send_message" || c.Name == "ask" {
			return d, true
		}
	}
	return d, false
}

// cutSeen is the seq a model call (a reply or a look) had in view, or -1.
func cutSeen(ev event.Event) int64 {
	switch ev.Type {
	case event.Assistant:
		var d event.AssistantData
		if ev.Decode(&d) == nil {
			return d.SeenSeq
		}
	case event.Checks:
		var d struct {
			SeenSeq int64 `json:"seen_seq"`
		}
		if ev.Decode(&d) == nil {
			return d.SeenSeq
		}
	}
	return -1
}

// cutWrite puts evs in a fresh project as session sid and returns its path.
func cutWrite(t *testing.T, sid string, evs []event.Event) (project, path string) {
	t.Helper()
	project = t.TempDir()
	path = filepath.Join(store.Root(project), sid)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf []byte
	for _, ev := range evs {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		buf = append(append(buf, raw...), '\n')
	}
	if err := os.WriteFile(filepath.Join(path, "0001.jsonl"), buf, 0o644); err != nil {
		t.Fatal(err)
	}
	return project, path
}

// cutCheck resumes every prefix of evs and reports what each cut broke.
func cutCheck(t *testing.T, sid string, evs []event.Event, model *scripted) {
	t.Helper()
	for cut := 1; cut <= len(evs); cut++ {
		prefix := evs[:cut]
		project, path := cutWrite(t, sid, prefix)
		s, _ := coreResume(t, path, project, model, newFakeSeat("ok"))
		s.settled()
		s.stop()
		after, err := store.Read(path)
		at := fmt.Sprintf("cut after %s (event %d of %d)", prefix[cut-1].Type, cut, len(evs))
		if err != nil {
			t.Errorf("%s: the resumed record does not read: %v", at, err)
			continue
		}
		for i := 1; i < len(after); i++ {
			if after[i].Seq <= after[i-1].Seq {
				t.Errorf("%s: seq %d follows %d (%s after %s)", at, after[i].Seq, after[i-1].Seq, after[i].Type, after[i-1].Type)
			}
		}
		// Every message typed into the agent has exactly one send of it.
		for _, u := range after {
			var ud event.UserMessageData
			if u.Type != event.UserMessage || u.Decode(&ud) != nil || ud.Held != "" {
				continue
			}
			sends := 0
			for _, ev := range after {
				var d event.RelaySendData
				if ev.Type == event.RelaySend && ev.Decode(&d) == nil && d.Auto && d.From == "user" && d.Seq == u.Seq {
					sends++
				}
			}
			if sends != 1 {
				t.Errorf("%s: user message seq %d, typed into the agent, has %d sends", at, u.Seq, sends)
			}
		}
		var users []event.Event
		for _, ev := range prefix {
			if ev.Type == event.UserMessage {
				users = append(users, ev)
			}
		}
		for _, u := range users {
			shown := false
			for _, ev := range after {
				if cutSeen(ev) >= u.Seq {
					shown = true
				}
			}
			if !shown {
				t.Errorf("%s: user message seq %d was passed over (never in a model call's view)", at, u.Seq)
			}
		}
		if len(users) == 0 {
			continue
		}
		last := users[len(users)-1].Seq
		before := 0
		for _, ev := range prefix {
			if d, ok := cutSends(ev); ok && d.SeenSeq >= last {
				before++
			}
		}
		again := 0
		for _, ev := range after {
			if ev.Seq <= prefix[cut-1].Seq {
				continue
			}
			if d, ok := cutSends(ev); ok && d.SeenSeq >= last {
				again++
			}
		}
		if before > 0 && again > 0 {
			t.Errorf("%s: the reply to user message seq %d was in the prefix and the resumed session replied %d more time(s)", at, last, again)
		}
	}
}

// cutRecord stops s, waits for it to end and returns its finished record.
func cutRecord(t *testing.T, s *session) (string, []event.Event) {
	t.Helper()
	path := s.rt.sess.Path
	s.settled()
	s.stop()
	evs, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Base(path), evs
}

func cutSentCount(s *session) int {
	n := 0
	for _, ev := range s.events(event.Assistant) {
		if _, ok := cutSends(ev); ok {
			n++
		}
	}
	return n
}

// (a) two user messages, each answered with a plain reply.
func TestCutEveryWriteTwoPlainReplies(t *testing.T) {
	s := startSession(t, "", cutBrain, nil)
	s.settled()
	s.type_("@e cutq one")
	s.waitFor("the first reply", func() bool { return cutSentCount(s) >= 1 })
	s.settled()
	s.type_("@e cutq two")
	s.waitFor("the second reply", func() bool { return cutSentCount(s) >= 2 })
	sid, evs := cutRecord(t, s)
	cutCheck(t, sid, evs, s.model)
}

// (c) a user message typed into the running agent, and acknowledged.
func TestCutEveryWriteMessageToTheAgent(t *testing.T) {
	s := startSession(t, brief, cutBrain, nil)
	s.waitFor("the brief to reach the agent", func() bool { return len(s.seat.sentTexts()) == 1 })
	s.settled()
	s.type_("cutq one")
	s.waitFor("the message to reach the agent", func() bool { return len(s.seat.sentTexts()) == 2 })
	s.waitFor("the acknowledgement", func() bool { return cutSentCount(s) >= 1 })
	sid, evs := cutRecord(t, s)
	cutCheck(t, sid, evs, s.model)
}

// (b) a user message that arrives while a model call is out.
func TestCutEveryWriteMessageDuringACall(t *testing.T) {
	release := make(chan struct{})
	var held atomic.Bool
	s := startSession(t, "", func(m string, msgs []map[string]any) reply {
		r := cutBrain(m, msgs)
		if len(r.calls) == 1 && r.calls[0].Name == "send_message" && held.CompareAndSwap(false, true) {
			r.block = release
		}
		return r
	}, nil)
	s.settled()
	s.type_("@e cutq one")
	s.waitFor("the call to be out", held.Load)
	s.type_("@e cutq two")
	s.waitFor("the second message", func() bool { return len(s.events(event.UserMessage)) == 2 })
	close(release)
	s.waitFor("the replies", func() bool { return cutSentCount(s) >= 2 })
	sid, evs := cutRecord(t, s)
	cutCheck(t, sid, evs, s.model)
}
