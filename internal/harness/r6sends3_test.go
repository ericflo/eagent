package harness

import (
	"context"
	"errors"
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

// One user message sent to two agents has one receipt. Once an agent has
// it, a send elsewhere that is on its way or fails does not take that
// back; a failure first and an arrival after ends delivered too. Late
// confirmation of an unconfirmed send turns it into delivered.
func TestOneReceiptForAMessageSentToTwoAgents(t *testing.T) {
	for _, c := range []struct {
		name  string
		steps []string
		want  string
	}{
		{"arrived at the first, failed at the second", []string{"sent", "delivered", "sent", "failed"}, "delivered"},
		{"failed at the first, arrived at the second", []string{"sent", "failed", "sent", "delivered"}, "delivered"},
		{"failed at both", []string{"sent", "failed", "sent", "failed"}, "failed"},
		{"unconfirmed, then confirmed late", []string{"sent", "sent", "delivered"}, "delivered"},
		{"unconfirmed", []string{"sent", "sent"}, "sent"},
	} {
		got := ""
		for _, s := range c.steps {
			got = mergeReceipt(got, s)
		}
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// The same through the record, as a resumed session rebuilds it.
	rec := &relayRecord{t: time.Now()}
	rec.start("codex", "codex")
	rec.start("claude", "claude")
	seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
	rec.send("codex", "r1", seq)
	rec.add(event.RelayDelivered, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: true})
	rec.send("claude", "r2", seq)
	rec.failed("claude", "r2", event.DeliveryNotSent)
	p := &phone{}
	p.loadReceipts(&Runtime{st: state.Replay(rec.evs)})
	if len(p.receipts) != 1 || p.receipts[0].status != "delivered" {
		t.Errorf("rebuilt: %+v", p.receipts)
	}
}

// "The same agent" is the agent that holds the name now, by the one
// definition the brief uses too (holderAt): a fresh start under a stopped
// name is another agent and may be sent the message; a restart, or a start
// that continues the agent's own conversation, is the same one and has it.
// A stop and resume of the session replays the same answer.
func TestForwardingAgainCountsOnlyTheAgentHoldingTheNameNow(t *testing.T) {
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
	for _, c := range []struct {
		name    string
		after   []step
		refused bool
	}{
		{"the same agent", nil, true},
		{"stopped, then started fresh", []step{stop, fresh}, false},
		{"restarted, continuing its conversation", []step{continues}, true},
		{"stopped, then started again on its own conversation", []step{stop, continues}, true},
		{"a switch away and back", []step{switchAway, switchBack}, false},
	} {
		rec := &relayRecord{t: time.Now()}
		rec.start("codex", "codex")
		seq := rec.add(event.UserMessage, event.UserMessageData{Text: "check the version"})
		rec.send("codex", "r1", seq)
		rec.add(event.RelayDelivered, event.RelayDeliveredData{Name: "codex", ID: "r1", OK: true})
		for _, s := range c.after {
			s(rec)
		}
		// Replayed from the record: what a resumed session decides.
		id, why := forwardedAs(state.Replay(rec.evs), "codex", seq, "")
		if refused := id != ""; refused != c.refused {
			t.Errorf("%s: refused %v (%q), want %v", c.name, refused, why, c.refused)
		}
	}
}
