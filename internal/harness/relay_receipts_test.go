package harness

import (
	"context"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// Receipts are rebuilt from the record when a session resumes: the first
// patch after the resume carries the receipts set before it, not only the
// new one (each patch replaces the thread's whole set), and a send made
// before the resume is still matched to its message.
func TestRelayReceiptsSurviveAResume(t *testing.T) {
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

	// Resumed, with the agent still running.
	useFakeRelays(t, newFakeSeat("Updated the docs."))
	gets := fp.getCount()
	rt2, err := Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 4)}, path)
	if err != nil {
		t.Fatal(err)
	}
	relayRunLive(t, rt2)
	var sendID string
	var seq int64
	rt2.sync(func() {
		for _, ev := range rt2.st.Events {
			if ev.Type == event.RelaySend {
				var d event.RelaySendData
				_ = ev.Decode(&d)
				sendID, seq = d.ID, d.Seq
			}
		}
	})
	waitFor(t, "the resumed phone poll", func() bool { return fp.getCount() > gets })
	fp.userReply("Now update the docs")
	second := fp.lastMessageID()
	waitFor(t, "the second message's receipt", func() bool { return lastReceipts()[second] == "delivered" })
	if got := lastReceipts(); got[first] != "delivered" {
		t.Fatalf("the receipt from before the resume was dropped: %v", got)
	}
	rt2.sync(func() {
		p := rt2.phone
		if p == nil {
			t.Error("no phone mirror")
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.sendSeq[sendID] != seq || seq == 0 {
			t.Errorf("send %s before the resume not matched to message %d: %v", sendID, seq, p.sendSeq)
		}
	})
}
