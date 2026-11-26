package harness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// The rule for a send, row by row: only a send that certainly did not go
// may be forwarded again, and only one that arrived is a delivered receipt.
func TestJudgeSendRule(t *testing.T) {
	for _, c := range []struct {
		o                state.SendOutcome
		listed           string
		again            bool
		refusal, receipt string
	}{
		{state.SendWaiting, "not confirmed yet", false, "already on its way to Codex", "sent"},
		{state.SendHeld, "held for the end of its turn", false, "already held for Codex", "held"},
		{state.SendArrived, "arrived", false, "already went to Codex as written (r1)", "delivered"},
		{"", "arrived", false, "already went to Codex as written (r1)", "delivered"},
		{state.SendNotSent, "did not go", true, "", "failed"},
		{state.SendUnconfirmed, "never confirmed", false, "Look at relay_screen first", "sent"},
		{sendMoved, "went to Claude", false, "went to Claude instead (r1)", ""},
	} {
		v := judgeSend(c.o, "Codex", "r1", "Claude", "\n\n[The user attached plan.pdf: /w/plan.pdf]")
		if !strings.Contains(v.listed, c.listed) || v.again != c.again || v.receipt != c.receipt {
			t.Errorf("%q: %+v", c.o, v)
		}
		if c.refusal != "" && !strings.Contains(v.refusal, c.refusal) || c.again && v.refusal != "" {
			t.Errorf("%q refusal: %q", c.o, v.refusal)
		}
		if c.o != state.SendArrived && c.o != "" && strings.Contains(v.refusal, "already went") {
			t.Errorf("%q says it already went: %q", c.o, v.refusal)
		}
	}
	if v := judgeSend(state.SendUnconfirmed, "Codex", "r1", "", "\n\n[The user attached plan.pdf: /w/plan.pdf]"); !strings.Contains(v.refusal, "/w/plan.pdf") {
		t.Errorf("an unconfirmed send's refusal loses the attachments: %q", v.refusal)
	}
}

// A seat fails before it types, except while typing.
func TestSendErrOutcome(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{errors.New("the agent is showing something that needs an answer first"), event.DeliveryNotSent},
		{relay.ErrClosed, event.DeliveryNotSent},
		{fmt.Errorf("the agent could not start again: %w", errors.New("x")), event.DeliveryNotSent},
		{&relay.TypingError{Err: errors.New("paste failed")}, event.DeliveryUnconfirmed},
		{fmt.Errorf("wrapped: %w", &relay.TypingError{Err: errors.New("enter failed")}), event.DeliveryUnconfirmed},
	} {
		if got := sendErrOutcome(c.err); got != c.want {
			t.Errorf("%v: %q, want %q", c.err, got, c.want)
		}
	}
}

// A retry that fails again is shown as a second failure, and a third try
// is allowed.
func TestARetryThatFailsAgainAllowsAThird(t *testing.T) {
	seat := newFakeSeat("done")
	seat.sendErr = errors.New("the agent is showing something that needs an answer first")
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	seq := userSaysWith(t, rt, "check the version", plan)
	waitFor(t, "the first failure", func() bool { return len(failedDeliveries(rt)) == 1 })
	if text, isErr := forward(rt, map[string]any{"seq": seq}); !isErr || !strings.Contains(text, "Could not send") {
		t.Fatalf("the retry: %q", text)
	}
	waitFor(t, "the second failure", func() bool { return len(failedDeliveries(rt)) == 2 })
	if s := sendsOf(rt, "codex"); strings.Count(s, "did not go") != 2 {
		t.Errorf("both failures listed: %q", s)
	}
	seat.setSendErr(nil)
	if text, isErr := forward(rt, map[string]any{"seq": seq}); isErr {
		t.Fatalf("the third try: %q", text)
	}
	if sent := seat.sentTexts(); len(sent) != 1 || !strings.Contains(sent[0], "/w/uploads/plan.pdf") {
		t.Errorf("typed: %q", sent)
	}
}

// relayRecord is a record of relay events, replayed as a resumed session
// replays it.
type relayRecord struct {
	evs []event.Event
	t   time.Time
}

func (r *relayRecord) add(typ string, d any) int64 {
	ev := event.New(typ, event.ActorHarness, d)
	ev.Seq = int64(len(r.evs) + 1)
	r.t = r.t.Add(time.Second)
	ev.Time = r.t
	r.evs = append(r.evs, ev)
	return ev.Seq
}

func (r *relayRecord) start(name, agent string) {
	r.add(event.RelayStart, event.RelayStartData{Name: name, Profile: name, Agent: agent, AgentLabel: strings.ToUpper(agent[:1]) + agent[1:], Dir: "/p"})
}

func (r *relayRecord) send(name, id string, seq int64) {
	r.add(event.RelaySend, event.RelaySendData{Name: name, ID: id, From: "user", Seq: seq, Mode: "queue", Text: "check the version"})
}

func (r *relayRecord) failed(name, id, outcome string) {
	r.add(event.RelayDelivered, event.RelayDeliveredData{Name: name, ID: id, OK: false, Outcome: outcome, Detail: "x"})
}

// What the user sent to one agent says nothing about another: a message
// that failed to go to Codex can be forwarded to Claude, and so can one
// that reached Codex (as before). The record alone decides, so a stop and
// resume between the failure and the retry changes nothing.
func TestForwardingAgainIsDecidedPerAgentFromTheRecord(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	rec.start("claude", "claude")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	rec.failed("codex", "r1", event.DeliveryNotSent)
	st := state.Replay(rec.evs)
	if id, why := forwardedAs(st, "codex", seq, ""); id != "" {
		t.Errorf("codex, after a send that did not go: %s %q", id, why)
	}
	if id, _ := forwardedAs(st, "claude", seq, ""); id != "" {
		t.Errorf("claude was never sent it: %s", id)
	}
	rec.failed("codex", "r1", "") // and an older record: may have gone
	st = state.Replay(rec.evs)
	if id, why := forwardedAs(st, "codex", seq, ""); id != "r1" || !strings.Contains(why, "relay_screen") {
		t.Errorf("codex, after an unconfirmed send: %s %q", id, why)
	}
	if id, _ := forwardedAs(st, "claude", seq, ""); id != "" {
		t.Errorf("claude, after an unconfirmed send to codex: %s", id)
	}
}

// A send that waited for a switch and went to the agent that took over is
// that agent's: the old agent's list says where it went and refuses it
// there; the new agent's says how it ended there, and its outcome there
// decides.
func TestAMovedSendIsJudgedByWhereItWent(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	rec.add(event.RelayEnd, event.RelayEndData{Name: "codex", Reason: "handed over to claude"})
	rec.start("claude", "claude")
	rec.add(event.RelayMoved, event.RelayMovedData{RelaySendData: event.RelaySendData{Name: "claude", ID: "r1", From: "user", Seq: seq, Mode: "queue", Text: "check the version"}, Prev: "codex"})
	rec.failed("claude", "r1", event.DeliveryNotSent)
	st := state.Replay(rec.evs)
	if s := strings.Join(userSends(st, "codex", 8), "\n"); !strings.Contains(s, "went to Claude") {
		t.Errorf("codex's list: %q", s)
	}
	if s := strings.Join(userSends(st, "claude", 8), "\n"); !strings.Contains(s, "did not go") {
		t.Errorf("claude's list: %q", s)
	}
	if id, why := forwardedAs(st, "codex", seq, ""); id != "r1" || !strings.Contains(why, "went to Claude instead") {
		t.Errorf("codex: %s %q", id, why)
	}
	if id, why := forwardedAs(st, "claude", seq, ""); id != "" {
		t.Errorf("claude, where it did not go: %s %q", id, why)
	}
}

// A handoff brief that could not be typed is on the record as not sent.
func TestAFailedBriefIsRecordedNotSent(t *testing.T) {
	old, next := &lingerSeat{newFakeSeat("one")}, newFakeSeat("two")
	next.sendErr = errors.New("the agent has exited")
	g := useGatedLaunches(t, []relaySeat{old, next}, nil)
	rt := newLaunchRuntime(t)
	out := relayToolAsync(rt, map[string]any{"action": "switch", "profile": "claude"})
	g.waitEntered(t)
	g.release <- struct{}{}
	recv(t, out, "switch")
	brief := lastSendID(rt)
	waitFor(t, "the brief recorded", func() bool { _, ok := failedDeliveries(rt)[brief]; return ok })
	if d := failedDeliveries(rt)[brief]; d.Outcome != event.DeliveryNotSent {
		t.Errorf("brief outcome: %+v", d)
	}
	rt.sync(func() {
		if o := rt.st.SendOutcome(brief); o != state.SendNotSent {
			t.Errorf("brief: %q", o)
		}
	})
}

// A message forwarded again after a send that did not go is one
// instruction to the agent taking over: its brief quotes it once.
func TestABriefQuotesAMessageSentTwiceOnce(t *testing.T) {
	seat := newFakeSeat("done")
	seat.sendErr = errors.New("the agent is showing something that needs an answer first")
	useGatedLaunches(t, []relaySeat{seat}, nil)
	rt := newLaunchRuntime(t)
	seq := userSaysWith(t, rt, "check the version")
	waitFor(t, "the failure", func() bool { return len(failedDeliveries(rt)) == 1 })
	seat.setSendErr(nil)
	if text, isErr := forward(rt, map[string]any{"seq": seq}); isErr {
		t.Fatalf("retry: %q", text)
	}
	rt.sync(func() {
		if brief := rt.handoffBrief(rt.st.Relays["codex"]); strings.Count(brief, "check the version") != 1 {
			t.Errorf("brief:\n%s", brief)
		}
	})
}

// The phone's receipt for a message that certainly did not go says so
// ("Not delivered", with Retry), never "delivered", and the receipts a
// resumed session rebuilds from the record agree. The live receipt guards
// behaviour older than send outcomes (any failure was "failed"); the
// rebuilt one is checked against it since outcomes made the two paths read
// the record through judgeSend.
func TestThePhoneReceiptForAMessageThatDidNotGo(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("done")
	seat.sendErr = errors.New("the agent is showing something that needs an answer first")
	useFakeRelays(t, seat)
	fp := newRelayPhone()
	t.Cleanup(fp.srv.Close)
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	project := t.TempDir()
	cfg := relayPhoneConfig(t, project, model.srv.URL, fp)
	rt, err := New(cfg, Options{Project: project, Relay: "codex", Interactive: true}, &fakeUI{input: make(chan string, 4)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	done := make(chan struct{})
	go func() { rt.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	fp.userReply("check the version")
	id := fp.lastMessageID()
	receipt := func() any {
		var got any
		for _, m := range fp.metaPatches() {
			if rr, ok := m["relay_receipts"].(map[string]any); ok {
				got = rr[id]
			}
		}
		return got
	}
	waitFor(t, "a failed receipt", func() bool { return receipt() == "failed" })
	for _, m := range fp.metaPatches() {
		if rr, ok := m["relay_receipts"].(map[string]any); ok && rr[id] == "delivered" {
			t.Fatalf("a receipt said delivered: %v", rr)
		}
	}
	if live, rebuilt := receiptsLiveAndRebuilt(rt); live != rebuilt || live != "failed" {
		t.Errorf("live %q, rebuilt from the record %q", live, rebuilt)
	}
}

// receiptsLiveAndRebuilt is the newest message's receipt as the running
// phone holds it and as a resumed session would rebuild it from the record.
func receiptsLiveAndRebuilt(rt *Runtime) (live, rebuilt string) {
	rt.sync(func() {
		last := func(p *phone) string {
			p.mu.Lock()
			defer p.mu.Unlock()
			if n := len(p.receipts); n > 0 {
				return p.receipts[n-1].status
			}
			return ""
		}
		live = last(rt.phone)
		fresh := &phone{}
		fresh.loadReceipts(rt)
		rebuilt = last(fresh)
	})
	return live, rebuilt
}
