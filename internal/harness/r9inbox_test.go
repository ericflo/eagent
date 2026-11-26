package harness

import (
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Finding 6 of the ninth review, first half: a stop and a message in the
// inbox at one poll. The stop ends the session; the message is left for
// the next run, which takes it (it used to be taken by the ending session,
// whose send was never typed).
func TestR9InboxWordsAfterAStopAreLeftForTheNextRun(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "stop", From: "web"}); err != nil {
		t.Fatal(err)
	}
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "r9 ship the fix", From: "web"}); err != nil {
		t.Fatal(err)
	}
	rt.pollInbox()
	if !rt.ending {
		t.Fatalf("the stop was not carried out")
	}
	if got := r8cWords(rt, event.UserMessage, "r9 ship the fix"); len(got) != 0 {
		t.Fatalf("the ending session took the message: %q", got)
	}
	path := rt.sess.Path
	if entries, _ := os.ReadDir(InboxDir(path)); len(entries) != 1 {
		t.Fatalf("inbox after the stop: %v, want the message's file", entries)
	}
	rt.sess.Close()
	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: rt.opts.Project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.sess.Close()
	rt2.pollInbox()
	if got := r8cWords(rt2, event.UserMessage, "r9 ship the fix"); len(got) != 1 {
		t.Fatalf("the next run took the message %d times, want once", len(got))
	}
}

// Second half: the record cannot be written when a message from eagent
// serve is taken. Its file stays, so the message is not lost.
func TestR9InboxWordsWhoseWriteFailedKeepTheirFile(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	r8cPost(t, rt, InboxMessage{Type: "message", Text: "r9 kept", From: "web"})
	rt.sess.Close()
	rt.pollInbox()
	if !rt.ending {
		t.Fatalf("a failed write did not end the session")
	}
	if entries, _ := os.ReadDir(InboxDir(rt.sess.Path)); len(entries) != 1 {
		t.Fatalf("inbox after a failed write: %v, want the message's file", entries)
	}
}
