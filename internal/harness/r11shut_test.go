package harness

import (
	"context"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/state"
	"github.com/ericflo/eagent/internal/store"
)

// r11PhoneSession runs a phone session at a terminal whose agent (codex)
// is seat; the phone's user messages go to it.
func r11PhoneSession(t *testing.T, seat relaySeat) (*Runtime, *relayPhone, <-chan int) {
	t.Helper()
	useGatedLaunches(t, []relaySeat{seat}, nil)
	t.Setenv("EAGENT_TEST_KEY", "x")
	relayAgentHomes(t)
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	done := make(chan int, 1)
	go func() { done <- rt.Run(ctx) }()
	rt.sync(func() { rt.st.Relays["codex"].State = "idle" })
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	return rt, fp, done
}

// r11Receipt is the last receipt the phone was given for its message id.
func r11Receipt(fp *relayPhone, id string) string {
	got := ""
	for _, m := range fp.metaPatches() {
		if rr, ok := m["relay_receipts"].(map[string]any); ok {
			if s, ok := rr[id].(string); ok {
				got = s
			}
		}
	}
	return got
}

// Finding 6. Two phone messages for the agent: the first is being typed
// when the session quits, the second waits behind it. The second was never
// typed; the record says not_sent, and the phone's receipt for it says
// failed (a Retry), not the "sent" it showed while it waited.
func TestR11AQueuedPhoneSendNotSentAtTheEndFailsOnThePhone(t *testing.T) {
	seat := &heldSendSeat{fakeSeat: newFakeSeat("one"), entered: make(chan string, 4), result: make(chan error, 4)}
	rt, fp, done := r11PhoneSession(t, seat)
	fp.userReply("check the version")
	select {
	case <-seat.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first send was never typed")
	}
	typing := lastSendID(rt)
	fp.userReply("then run the tests")
	second := fp.lastMessageID()
	waitFor(t, "the second send queued", func() bool { return lastSendID(rt) != typing })
	queued := lastSendID(rt)
	waitFor(t, "the second message's receipt", func() bool { return r11Receipt(fp, second) != "" })
	rt.sync(func() { rt.beginShutdown("quit", 0) })
	select {
	case <-rt.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never closed")
	}
	seat.result <- nil
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
	}
	evs, err := store.Read(rt.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	if o := state.Replay(evs).SendOutcome(queued); o != state.SendNotSent {
		t.Fatalf("the queued send reads %q on the record, want not_sent", o)
	}
	if got := r11Receipt(fp, second); got != "failed" {
		t.Errorf("the phone's receipt for the send recorded not_sent is %q, want failed", got)
	}
}
