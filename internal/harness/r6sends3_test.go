package harness

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/state"
)

// A send typed and never confirmed may be in the agent's hands. The phone
// must not call it "Not delivered", whose Retry sends the words again: it
// stays "sent" (typed into the agent), as relay_forward refuses it blind.
func TestAnUnconfirmedSendStaysSentOnThePhone(t *testing.T) {
	if v := judgeSend(state.SendUnconfirmed, "Codex", "r1", "", ""); v.receipt != "sent" {
		t.Errorf("unconfirmed send: receipt %q", v.receipt)
	}
}

// End to end: typing fails half way, the record says it may have gone, and
// the phone shows it as sent, live and as rebuilt after a resume.
func TestThePhoneShowsATypingFailureAsSent(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("done")
	seat.sendErr = &relay.TypingError{Err: errors.New("send-keys Enter: server exited")}
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
	waitFor(t, "the failure on the record", func() bool { return len(failedDeliveries(rt)) == 1 })
	for _, d := range failedDeliveries(rt) {
		if d.Outcome != event.DeliveryUnconfirmed {
			t.Fatalf("outcome %q, want unconfirmed", d.Outcome)
		}
	}
	// The receipt is set on the loop as the failure is recorded.
	if live, rebuilt := receiptsLiveAndRebuilt(rt); live != "sent" || rebuilt != "sent" {
		t.Errorf("live %q, rebuilt %q; want sent for both", live, rebuilt)
	}
}

// The receipt rule, row by row.
func TestMessageReceiptRule(t *testing.T) {
	a, w, h, u, n := state.SendArrived, state.SendWaiting, state.SendHeld, state.SendUnconfirmed, state.SendNotSent
	for _, c := range []struct {
		sends []state.SendOutcome
		want  string
	}{
		{nil, ""},
		{[]state.SendOutcome{a}, "delivered"},
		{[]state.SendOutcome{n, a}, "delivered"},
		{[]state.SendOutcome{u, n}, "sent"},
		{[]state.SendOutcome{w, n}, "sent"},
		{[]state.SendOutcome{h, n}, "held"},
		{[]state.SendOutcome{h, u}, "sent"},
		{[]state.SendOutcome{n, n, n}, "failed"},
		{[]state.SendOutcome{""}, "sent"},
	} {
		if got := messageReceipt(c.sends); got != c.want {
			t.Errorf("%v: %q, want %q", c.sends, got, c.want)
		}
	}
}

// receiptWant is the rule written out for the test, from the sends'
// final outcomes.
func receiptWant(os []state.SendOutcome) string {
	has := func(want ...state.SendOutcome) bool {
		for _, o := range os {
			for _, w := range want {
				if o == w {
					return true
				}
			}
		}
		return false
	}
	switch {
	case has(state.SendArrived):
		return "delivered"
	case has(state.SendWaiting, state.SendUnconfirmed):
		return "sent"
	case has(state.SendHeld):
		return "held"
	}
	return "failed"
}

func permutations(xs []state.SendOutcome) [][]state.SendOutcome {
	if len(xs) <= 1 {
		return [][]state.SendOutcome{append([]state.SendOutcome(nil), xs...)}
	}
	var out [][]state.SendOutcome
	for i := range xs {
		rest := append(append([]state.SendOutcome(nil), xs[:i]...), xs[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]state.SendOutcome{xs[i]}, p...))
		}
	}
	return out
}

// One user message sent two or three times, to different agents, in every
// order of every mix of outcomes: its one phone receipt follows the rule
// over all its sends, never just the newest event, and the receipt worked
// out live (after each event, as observeRelay does) agrees with the one a
// resumed session rebuilds from the record (loadReceipts). The reader's
// case, typed into Codex and never confirmed and then certainly failed at
// Claude, is the row [unconfirmed not_sent]: "sent", no Retry.
func TestOneReceiptForAMessageSentToSeveralAgents(t *testing.T) {
	kinds := []state.SendOutcome{state.SendArrived, state.SendWaiting, state.SendHeld, state.SendUnconfirmed, state.SendNotSent}
	var mixes [][]state.SendOutcome
	for i := range kinds {
		for j := i; j < len(kinds); j++ {
			mixes = append(mixes, []state.SendOutcome{kinds[i], kinds[j]})
			for k := j; k < len(kinds); k++ {
				mixes = append(mixes, []state.SendOutcome{kinds[i], kinds[j], kinds[k]})
			}
		}
	}
	agents := []string{"codex", "claude", "pi"}
	for _, mix := range mixes {
		for _, order := range permutations(mix) {
			rec := &relayRecord{t: time.Now()}
			for _, a := range agents {
				rec.start(a, a)
			}
			seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
			for i, o := range order {
				name, id := agents[i], fmt.Sprintf("r%d", i+1)
				mode := "queue"
				if o == state.SendHeld {
					mode = "after_turn"
				}
				rec.add(event.RelaySend, event.RelaySendData{Name: name, ID: id, From: "user", Seq: seq, Mode: mode, Text: "check the version"})
				switch o {
				case state.SendArrived:
					rec.add(event.RelayDelivered, event.RelayDeliveredData{Name: name, ID: id, OK: true})
				case state.SendUnconfirmed:
					rec.failed(name, id, event.DeliveryUnconfirmed)
				case state.SendNotSent:
					rec.failed(name, id, event.DeliveryNotSent)
				}
			}
			want := receiptWant(order)
			// Live: the state as it stood at each event, the receipt worked
			// out from it then.
			st := state.Replay(nil)
			sendSeq := map[string]int64{}
			live := ""
			for _, ev := range rec.evs {
				st.Apply(ev)
				var d struct {
					ID string `json:"id"`
				}
				switch ev.Type {
				case event.RelaySend:
					_ = ev.Decode(&d)
					sendSeq[d.ID] = seq
					live = sendsReceipt(st, sendSeq, seq)
				case event.RelayDelivered:
					live = sendsReceipt(st, sendSeq, seq)
				}
			}
			p := &phone{}
			p.loadReceipts(&Runtime{st: state.Replay(rec.evs)})
			rebuilt := ""
			if len(p.receipts) == 1 {
				rebuilt = p.receipts[0].status
			}
			if live != want || rebuilt != want {
				t.Errorf("%v: live %q, rebuilt %q, want %q", order, live, rebuilt, want)
			}
		}
	}
}

// A send a switch moved to another agent counts by its outcome there.
func TestAMovedSendCountsForTheReceiptByItsOutcomeWhereItWent(t *testing.T) {
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	rec.add(event.RelayEnd, event.RelayEndData{Name: "codex", Reason: "handed over to claude"})
	rec.start("claude", "claude")
	rec.add(event.RelayMoved, event.RelayMovedData{RelaySendData: event.RelaySendData{Name: "claude", ID: "r1", From: "user", Seq: seq, Mode: "queue", Text: "check the version"}, Prev: "codex"})
	rec.add(event.RelayDelivered, event.RelayDeliveredData{Name: "claude", ID: "r1", OK: true})
	p := &phone{}
	p.loadReceipts(&Runtime{st: state.Replay(rec.evs)})
	if len(p.receipts) != 1 || p.receipts[0].status != "delivered" {
		t.Errorf("rebuilt: %+v", p.receipts)
	}
}

// relay_forward's duplicate check counts every send of the message to any
// agent that held the relay's name, replayed from the record. An agent
// started fresh under a stopped name never saw the message as its own, yet
// forwarding it is refused (relay_send still sends it): counting only the
// agent holding the name now let a message be typed twice (after a switch
// back, whose brief quotes it, and for a send held while a fresh start
// launched). The refusal's words say so when the record proves it: every
// send arrived before the stop of the agent that held the name then, and
// the agent holding it now started after that stop.
func TestForwardingAgainCountsEverySendUnderTheName(t *testing.T) {
	const old = "that message already went to Codex as written (r1); to add to it, use relay_send"
	const stopped = "that message went as written (r1) to the agent that held the name codex before, which was stopped; Codex, running now, was not sent it as its own message (its hand-over brief may quote it); to give it to this one, send the words with relay_send"
	const waiting = "that message is already on its way to Codex as written (r1), and its transcript has not shown it yet; to add to it, use relay_send"
	const heldOld = "that message already went to Codex as written (r2); to add to it, use relay_send"
	type step func(r *relayRecord)
	stop := func(r *relayRecord) { r.add(event.RelayEnd, event.RelayEndData{Name: "codex", Reason: "stopped"}) }
	fresh := func(r *relayRecord) { r.start("codex", "codex") }
	continues := func(r *relayRecord) {
		r.add(event.RelayStart, event.RelayStartData{Name: "codex", Profile: "codex", Agent: "codex", Dir: "/p", Resumed: true})
	}
	switchAway := func(r *relayRecord) {
		r.add(event.RelayEnd, event.RelayEndData{Name: "codex", Reason: "handed over to claude"})
		r.start("claude", "claude")
	}
	switchBack := func(r *relayRecord) {
		r.add(event.RelayEnd, event.RelayEndData{Name: "claude", Reason: "handed over to codex"})
		r.start("codex", "codex")
	}
	var seq int64
	// heldForStart is a send recorded while the fresh agent started, then
	// typed into it and confirmed there.
	heldForStart := func(r *relayRecord) {
		r.send("codex", "r2", seq)
		r.start("codex", "codex")
		r.add(event.RelayDelivered, event.RelayDeliveredData{Name: "codex", ID: "r2", OK: true})
	}
	for _, c := range []struct {
		name        string
		unconfirmed bool // r1 is never confirmed
		after       []step
		refused     bool
		why         string
	}{
		{"the same agent", false, nil, true, old},
		{"stopped, then started fresh", false, []step{stop, fresh}, true, stopped},
		{"restarted, continuing its conversation", false, []step{continues}, true, old},
		{"stopped, then started again on its own conversation", false, []step{stop, continues}, true, old},
		{"a switch away and back", false, []step{switchAway, switchBack}, true, stopped},
		{"still unconfirmed, then stopped and started fresh", true, []step{stop, fresh}, true, waiting},
		{"stopped, then a send held while a fresh agent started", false, []step{stop, heldForStart}, true, heldOld},
	} {
		rec := &relayRecord{t: time.Now()}
		rec.start("codex", "codex")
		seq = rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
		rec.send("codex", "r1", seq)
		if !c.unconfirmed {
			rec.add(event.RelayDelivered, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: true})
		}
		for _, s := range c.after {
			s(rec)
		}
		// Replayed from the record: what a resumed session decides.
		id, why := forwardedAs(state.Replay(rec.evs), "codex", seq, "")
		if refused := id != ""; refused != c.refused {
			t.Errorf("%s: refused %v (%q), want %v", c.name, refused, why, c.refused)
		}
		if why != c.why {
			t.Errorf("%s: refusal\n %q\nwant\n %q", c.name, why, c.why)
		}
	}
}
