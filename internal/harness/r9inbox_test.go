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

// A message for the running agent taken while the record cannot be
// written: the ending run still types it, so its file goes as before and
// the next run does not type it again (a reader proved that keeping the
// file typed it twice).
func TestR9InboxWordsWhoseWriteFailedAreTypedOnce(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("ok")
	useFakeRelays(t, seat)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	const text = "r9 please run the tests"
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: text, From: "web"}); err != nil {
		t.Fatal(err)
	}
	path := rt.sess.Path
	rt.sess.Close()
	rt.pollInbox()
	if !rt.ending {
		t.Fatalf("a failed write did not end the session")
	}
	waitFor(t, "the ending run to type it", func() bool { return len(seat.sentTexts()) == 1 })
	close(rt.closed)
	if entries, _ := os.ReadDir(InboxDir(path)); len(entries) != 0 {
		t.Fatalf("inbox after the failed write: %v, want it empty", entries)
	}
	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: rt.opts.Project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.sess.Close()
	rt2.pollInbox()
	if got := r8cWords(rt2, event.UserMessage, text); len(got) != 0 {
		t.Fatalf("the next run took the message again: %q", got)
	}
}

// A command after a stop in the same poll is carried out by the ending
// run, as before: only words are left for the next one.
func TestR9InboxACommandAfterAStopIsNotLeft(t *testing.T) {
	ui := &r8cHelpUI{}
	rt := r8cNew(t, ui)
	ui.dir = InboxDir(rt.sess.Path)
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "stop", From: "web"}); err != nil {
		t.Fatal(err)
	}
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "/help"}); err != nil {
		t.Fatal(err)
	}
	rt.pollInbox()
	if !rt.ending || len(ui.files) != 1 {
		t.Fatalf("ending %v, /help carried out %d times; want the stop and /help once", rt.ending, len(ui.files))
	}
	if entries, _ := os.ReadDir(ui.dir); len(entries) != 0 {
		t.Fatalf("files left: %v", entries)
	}
}
