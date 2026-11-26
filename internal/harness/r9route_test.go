package harness

import (
	"strings"
	"testing"
	"time"
)

// A quit and a message for the running agent in one poll, then a resume:
// the next run is woken for the kept message and its model is told it was
// kept, not that it was passed on.
func TestRV9QuitThenMessageIsTakenUpByTheNextRun(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("ok")
	useFakeRelays(t, seat)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "/quit"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "rv9 ship it", From: "web"}); err != nil {
		t.Fatal(err)
	}
	rt.pollInbox()
	path := rt.sess.Path
	close(rt.closed)
	rt.sess.Close()
	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: rt.opts.Project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.sess.Close()
	if !rt2.wakePending() {
		t.Fatalf("the next run is not woken for the kept message")
	}
	tail := rt2.userTail("Codex")
	if !strings.Contains(tail, "kept for you (the session was ending)") || strings.Contains(tail, "typed into") || strings.Contains(tail, "could not be confirmed") {
		t.Fatalf("the next run's model is told: %q", tail)
	}
	if len(seat.sentTexts()) != 0 {
		t.Fatalf("typed: %q", seat.sentTexts())
	}
}
