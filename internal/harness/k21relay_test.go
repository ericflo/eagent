package harness

import (
	"context"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// A phone message was forwarded while Codex was starting again (its
// relay.send says held), and eagent was cut before the start came up. The
// resume settles it not_sent (state.NeverTyped), so its receipt is now
// "failed" (the one with a Retry). The phone is never told: the resume
// writes the outcome before the mirror exists (reattachRelays runs before
// startPhone), and nothing at the mirror's start publishes the receipts,
// so the phone keeps showing the message as it last was (sent) until some
// other message's receipt happens to change.
func TestSR12ResumeNeverTellsThePhoneAHeldSendWasNotSent(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("Fixed the flaky login test."))
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
	lastReceipts := func() map[string]any {
		var got map[string]any
		for _, m := range fp.metaPatches() {
			if rr, ok := m["relay_receipts"].(map[string]any); ok {
				got = rr
			}
		}
		return got
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	done := make(chan struct{})
	go func() { rt.Run(ctx); close(done) }()
	waitFor(t, "the phone poll", func() bool { return fp.getCount() > 0 })
	fp.userReply("Fix the flaky login test")
	first := fp.lastMessageID()
	waitFor(t, "a delivered receipt", func() bool { return lastReceipts()[first] == "delivered" })
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not stop")
	}
	path := rt.sess.Path

	// The record as a run cut during a restart leaves it: a phone message,
	// forwarded and held for the start, which never came up.
	sess, _, err := store.Open(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	um, err := sess.Append(event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: "and the docs", Source: "phone", PhoneID: "ph-held"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.RelaySend, event.ActorHarness, event.RelaySendData{Name: "codex", ID: "r90", From: "user", Seq: um.Seq, Mode: "queue", Text: "and the docs", Auto: true, Held: true})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()

	useFakeRelays(t, newFakeSeat("Updated the docs."))
	gets := fp.getCount()
	rt2, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt2)
	var outcome string
	rt2.sync(func() { outcome = string(rt2.st.SendOutcome("r90")) })
	if outcome != "not_sent" {
		t.Fatalf("setup: r90 after the resume %q; want not_sent", outcome)
	}
	// The resumed mirror has polled, and its worker has done everything
	// queued before this marker (it runs its queue in order).
	waitFor(t, "the resumed phone poll", func() bool { return fp.getCount() > gets })
	flushed := make(chan struct{})
	rt2.sync(func() {
		if rt2.phone == nil {
			t.Error("no phone mirror")
			close(flushed)
			return
		}
		rt2.phone.enqueue(func(context.Context) { close(flushed) })
	})
	select {
	case <-flushed:
	case <-time.After(20 * time.Second):
		t.Fatal("the mirror's worker did not get to the marker")
	}
	if got := lastReceipts()["ph-held"]; got != "failed" {
		t.Fatalf("the phone's receipt for the message the resume settled not_sent: %v; want failed (all receipts: %v)", got, lastReceipts())
	}
}
