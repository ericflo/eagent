package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// The reread notice is "not taken for an answer (saidAt and saidSeq stay
// as they were)" while the session runs. A session resumed from the same
// record must read it the same way: what the loop holds of what the user
// last heard is the same as a replay of the record gives.
func TestRereadNoticeReadsTheSameAfterAResume(t *testing.T) {
	p := r5StartPass(t)
	p.type_("yes")
	p.waitFor("the user told the session is being reread", func() bool { return p.rereadNotices() == 1 })
	var live, replayed time.Time
	p.rt.sync(func() {
		live = p.rt.turns.saidAt
		var again turnState
		again.init(p.rt.st)
		replayed = again.saidAt
	})
	if !live.Equal(replayed) {
		t.Fatalf("the user last heard from eagent at %s by the running loop, and at %s by a resume of the same record: the reread notice counts after a resume and not before",
			live.Format(time.RFC3339Nano), replayed.Format(time.RFC3339Nano))
	}
}

// Once no restart can make room (the restart that carried nothing was
// refused on its first call), that "stands until a call goes through". A
// session stopped and resumed in that state is in the same place: its
// next refused call must not start another pointless restart, nor tell
// the user again (toldFull is in the record so that a resume does not).
func TestNoRestartCanMakeRoomStandsAcrossAResume(t *testing.T) {
	old := errorRetryDelays
	errorRetryDelays = []time.Duration{5 * time.Millisecond}
	t.Cleanup(func() { errorRetryDelays = old })
	route := &r5PictureRoute{window: 60000, ceiling: 2048, picture: 80000}
	tune := func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	}
	s := startSession(t, "", route.brain(), tune)
	r5LogOnFailure(s)
	s.settled()
	route.refuseAll.Store(true)
	r5SendPicture(s, "@e what does this screenshot show?")
	r4Until(t, "the user to be told no restart can make room", func() bool { return r5Told(s, fullNotice) > 0 })
	calls := s.model.count(testModel)
	r4Until(t, "more refused calls", func() bool { return s.model.count(testModel) > calls+5 })
	before := len(s.events(event.Dossier))
	cfg, path, project := s.rt.cfg, s.rt.sess.Path, s.rt.opts.Project
	s.stop()

	s2 := audit2Resume(t, cfg, Options{Project: project, Interactive: true}, path, s.seat, s.model)
	r5LogOnFailure(s2)
	calls = s.model.count(testModel)
	s2.rt.sync(func() { s2.rt.userMessage(event.UserMessageData{Text: "still there?", Route: "eagent"}) })
	r4Until(t, "refused calls after the resume", func() bool { return s.model.count(testModel) > calls+20 })
	if n := len(s2.events(event.Dossier)); n != before {
		for _, ev := range s2.events(event.Dossier, event.SubsessionStart, event.Message, event.UserMessage) {
			t.Logf("record: %d %s %s %.140s", ev.Seq, ev.Type, ev.Actor, ev.Data)
		}
		t.Errorf("%d restarts after the resume (%d before it, %d after), and no restart could make room", n-before, before, n)
	}
	if n := r5Told(s2, fullNotice); n != 0 {
		t.Errorf("the user was told %d more times after the resume that no restart can make room", n)
	}
}

// A restart that carried nothing whole (what was unseen was too much to
// carry) and was refused on its first call holds its opening alone: no
// restart can make room. The reread notice the pass itself wrote, after
// the place the dossier covers, is not something a further restart could
// take in to make room.
func TestTheRereadNoticeDoesNotBuyAnotherRestart(t *testing.T) {
	old := errorRetryDelays
	errorRetryDelays = []time.Duration{5 * time.Millisecond}
	t.Cleanup(func() { errorRetryDelays = old })
	route := &r5PictureRoute{window: 60000, ceiling: 2048, picture: 80000}
	s := startSession(t, "", route.brain(), func(c *config.Config) {
		c.RolloverTokens = 0
		c.Model.ContextTokens, c.Model.MaxTokens = route.window, route.ceiling
	})
	r5LogOnFailure(s)
	s.settled()
	route.refuseAll.Store(true)
	carry := 0
	s.rt.sync(func() { carry = s.rt.carryBytes() })
	text := "@e " + strings.Repeat("the heron lands at dusk ", carry/24+50)
	s.rt.sync(func() { s.rt.userMessage(event.UserMessageData{Text: text, Route: "eagent"}) })
	r4Until(t, "the user to be told no restart can make room", func() bool { return r5Told(s, fullNotice) > 0 || len(s.events(event.Dossier)) > 4 })
	ds := s.dossiers()
	if len(ds) > 1 {
		for _, ev := range s.events() {
			if ev.Seq > ds[0].CoversSeq {
				t.Logf("after what the first dossier covers: %d %s %s %.120s", ev.Seq, ev.Type, ev.Actor, ev.Data)
			}
		}
		t.Fatalf("%d restarts: the first carried nothing (covers %d) and was refused on its first call, so the second could only rebuild the same opening", len(ds), ds[0].CoversSeq)
	}
}

// The ratio belongs to the route the call was made on. A reading that
// comes back after the route changed (a model picked in the thread's
// settings while the call was out) is the old route's, and is not taught
// to the new one.
func TestAReadingFromTheOldRouteIsNotTaughtToTheNewOne(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	plain := []llm.Message{{Role: "user", Text: strings.Repeat("go on ", 400)}}
	req := llm.Request{System: s.rt.system, Messages: plain}
	var oldEP llm.Endpoint
	s.rt.sync(func() {
		m := s.rt.models[event.ActorEagent]
		oldEP = m.client.Endpoint
		ep := oldEP
		ep.Model = "another-model"
		m.client = s.rt.newClient(ep, event.ActorEagent)
		s.rt.tokenScale = 0
	})
	// The call made on the old route answers now.
	resp := &llm.Response{BaseURL: oldEP.BaseURL, Model: oldEP.Model, Asked: oldEP.Model, Usage: event.Usage{Input: 3 * r5Estimate(s, plain)}}
	s.rt.calibrate(event.ActorEagent, oldEP, req, resp, nil)
	k := 0
	s.rt.sync(func() { k = s.rt.scale() })
	if k != 1000 {
		t.Fatalf("ratio %d on the new route, learned from a call the old route answered; want 1000", k)
	}
}

// Ordinary windows get the restart size they got before the floor: the floor the
// quarter floor added (a quarter of the room past the fixed part) never moves it.
func TestOrdinaryWindowsKeepTheirRestartSize(t *testing.T) {
	old := func(window, maxTokens, fixed int) int {
		n := config.AutoRollover(window, maxTokens)
		if window <= 0 {
			window = 200_000
		}
		if maxTokens <= 0 {
			maxTokens = 16384
		}
		room := window - maxTokens - max(fixed, 0)
		if room <= 0 {
			return n
		}
		limit := max(fixed, 0) + room*3/4
		if limit/100*100 > max(fixed, 0) {
			limit = limit / 100 * 100
		}
		return min(n, limit)
	}
	for _, window := range []int{128_000, 200_000, 256_000, 400_000, 1_000_000} {
		for _, reply := range []int{0, 8192, 16384, 32000, 64000, 128000} {
			for fixed := 0; fixed <= 40_000; fixed += 500 {
				if window-max(reply, 16384)-fixed < smallRoom {
					continue // a small route
				}
				if a, b := old(window, reply, fixed), config.AutoRolloverWithin(window, reply, fixed); a != b {
					t.Errorf("window %d, reply %d, fixed %d: %d before the floor, %d now", window, reply, fixed, a, b)
				}
			}
		}
	}
}
