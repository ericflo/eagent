package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// The same model at the same address written with a trailing slash is
// the route eagent is on, both to the loop (sameRoute: the model keeps
// its failures, restarts in a row and token ratio) and to the record (no
// move: the dossier's restarts in a row and the size of the first call
// stand), so a resumed run reads it as the running one did.
func TestK2AnAddressWithATrailingSlashIsNoMove(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	var before, after int64
	s.rt.sync(func() { before = s.rt.st.Moved() })
	conf := s.rt.cfg.Model
	old := conf.BaseURL
	conf.BaseURL += "/"
	eps, err := conf.Routes()
	if err != nil {
		t.Fatal(err)
	}
	moved, on := true, false
	s.rt.sync(func() {
		moved = !sameRoute(s.rt.models[event.ActorEagent].client.Endpoint, eps[0])
		s.rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: conf.Model}, eps: eps, conf: conf})
		after = s.rt.st.Moved()
		// A reply still out from before, from the address as it was
		// written then, came from the route in use.
		on = s.rt.st.OnRoute(event.ActorEagent, old, conf.Model)
	})
	if moved {
		t.Fatalf("setup: the loop takes %q for another address", eps[0].BaseURL)
	}
	if after != before {
		t.Fatalf("the record took %q for a move (seq %d), the loop for the same route", eps[0].BaseURL, after)
	}
	if !on {
		t.Fatalf("a reply from %q is not taken for the route in use, now written %q", old, eps[0].BaseURL)
	}
}

// C5: a session with no terminal stops its agent and reports; its closing
// look puts the conversation past the restart size. The session is ending,
// and nothing will be asked of that conversation again: no dossier pass is
// paid for before it ends.
func TestK2AnEndingBatchSessionPaysForNoDossier(t *testing.T) {
	var passes atomic.Int32
	_, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			passes.Add(1)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case m[len(m)-1]["role"] == "tool":
			return hold()
		case strings.Contains(lastUserText(m), "The session is ending now"):
			r := hold()
			r.prompt = 30000 // past the 20,000 restart size of testConfig
			return r
		}
		return stopsAndReports("The build is green; stopping Codex.")
	})
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	if code != 0 {
		t.Fatalf("exit %d; said %q", code, ui.said())
	}
	if n := passes.Load(); n != 0 {
		t.Fatalf("%d dossier passes paid for by a session that was ending", n)
	}
}

// C6: a dossier pass looks something up, and the call with what it read
// back is refused as too long. The results are taken out again and the
// writer is told to write without them: in the request that follows, that
// note is added to the request for the briefing (withNote), as the pass's
// other notes are, not a user message of its own right after it. (The
// conversation before the request is the record as rendered, which has
// user messages in a row of its own.)
func TestK2ADossierPassThatDropsItsLookupsSendsNoTwoUserMessagesInARow(t *testing.T) {
	var ready, written atomic.Bool
	var roles atomic.Value // the roles of the request after the drop
	brain := func(_ string, m []map[string]any) reply {
		if dossierRequest(m) != "" {
			last := m[len(m)-1]
			text, _ := last["content"].(string)
			switch {
			case last["role"] == "tool":
				return r7TooLong
			case strings.Contains(text, "did not fit in the model's context window") || written.Load():
				var rs []string
				for _, msg := range m {
					r, _ := msg["role"].(string)
					rs = append(rs, r)
				}
				roles.Store(rs)
				written.Store(true)
				p := places(dossierRequest(m))
				return reply{text: briefing(p[max(0, len(p)-3):])}
			}
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
		}
		r := hold()
		if ready.Load() {
			r.prompt = 30000 // past the 20,000 restart size of testConfig
		}
		return r
	}
	s := startSession(t, "", brain, nil)
	r5LogOnFailure(s)
	s.settled()
	ready.Store(true)
	r14AgentTurn(s, "The build is green.")
	r6Until(t, "the briefing written after the drop", written.Load)
	rs, _ := roles.Load().([]string)
	if n := len(rs); n < 2 || rs[n-1] == "user" && rs[n-2] == "user" {
		t.Fatalf("the note after the drop is a user message of its own after the request: %v", rs)
	}
}

// C7: the agent is stopped, then eagent's model fails twice running. The
// user is told so, and the notice does not say their messages still go
// straight to an agent: none is running, and what they write waits for
// eagent.
func TestK2TheDownNoticeWithNoAgentRunningSaysWhereMessagesGo(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer bool // the failing turns' first call comes back
	}{{"unreachable", false}, {"failing", true}} {
		t.Run(c.name, func(t *testing.T) {
			setFor(t, &errorRetryDelays, []time.Duration{0, time.Hour})
			var stopped, failing atomic.Bool
			brain := func(_ string, m []map[string]any) reply {
				switch {
				case !stopped.Load():
					stopped.Store(true)
					return reply{calls: []event.ToolCall{tc("relay_control", `{"action":"stop","reason":"the work is done"}`)}}
				case !failing.Load():
					return hold()
				case c.answer && m[len(m)-1]["role"] != "tool":
					return reply{calls: []event.ToolCall{tc("session_search", `{"query":"build"}`)}}
				}
				return k1BadRequest
			}
			s := startSession(t, "", brain, nil)
			r5LogOnFailure(s)
			s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "@e stop codex", Route: "eagent"}) })
			r6Until(t, "the agent stopped", func() bool {
				n := 1
				s.rt.sync(func() { n = len(s.rt.st.ActiveRelays()) })
				return n == 0
			})
			s.settled()
			failing.Store(true)
			s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: "what now?", Route: "eagent"}) })
			r6Until(t, "the notice", func() bool { return r4Logged(s, "trying again in") > 0 })
			for _, m := range s.ui.said() {
				if strings.Contains(m, "can't reach my own model") || strings.HasPrefix(m, failingNotice) {
					if strings.Contains(m, "straight to") {
						t.Fatalf("with no agent running, told: %q", m)
					}
					return
				}
			}
			t.Fatalf("no notice; said %q", s.ui.said())
		})
	}
}
