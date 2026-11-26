package harness

import (
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Finding 6 of the ninth review: a quit and a message for the running
// agent in the inbox at one poll. The message is taken by the ending run,
// in order (an answer still answers its question), but kept for eagent:
// nothing more is typed once the session is ending, so no send is recorded
// that the next run would report as perhaps arrived. (Two independent
// reads proved that leaving it for the next run put it after that run's
// own words.)
func TestR9InboxWordsAfterAStopAreKeptNotPassedOn(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	seat := newFakeSeat("ok")
	useFakeRelays(t, seat)
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Relay: "codex"}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	// A quit (from a script) leaves the agents running, unlike a stop.
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "/quit"}); err != nil {
		t.Fatal(err)
	}
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "r9 ship the fix", From: "web"}); err != nil {
		t.Fatal(err)
	}
	rt.pollInbox()
	if !rt.ending || len(rt.st.ActiveRelays()) != 1 {
		t.Fatalf("ending %v, %d agents running; want the quit carried out and the agent left running", rt.ending, len(rt.st.ActiveRelays()))
	}
	var msgs []event.UserMessageData
	for _, ev := range rt.st.Events {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == "r9 ship the fix" {
			msgs = append(msgs, d)
		}
		var sd event.RelaySendData
		if ev.Type == event.RelaySend && ev.Decode(&sd) == nil && sd.Text == "r9 ship the fix" {
			t.Fatalf("a send was recorded for the message while the session was ending")
		}
	}
	if len(msgs) != 1 || msgs[0].Held != "the session was ending" {
		t.Fatalf("the message was recorded as %+v, want once, kept for eagent", msgs)
	}
	if entries, _ := os.ReadDir(InboxDir(rt.sess.Path)); len(entries) != 0 {
		t.Fatalf("files left: %v", entries)
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
