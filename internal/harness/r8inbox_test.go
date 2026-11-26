package harness

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// An inbox file is taken exactly once: the user.message or user.answer it
// becomes is written before the file is removed and names the file, so a
// removal that fails (or a process cut before it) leaves a file a later
// poll or run recognises as already taken.

// r8cUI notes, at each user.message or user.answer written, whether the
// inbox file it names was still in the inbox.
type r8cUI struct {
	fakeUI
	mu      sync.Mutex
	dir     string
	present []bool
}

func (u *r8cUI) Trace(ev event.Event) {
	if ev.Type != event.UserMessage && ev.Type != event.UserAnswer {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.dir == "" {
		return
	}
	entries, _ := os.ReadDir(u.dir)
	u.present = append(u.present, len(entries) > 0)
}

func r8cNew(t *testing.T, ui UI) *Runtime {
	t.Helper()
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir()}, ui)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	return rt
}

// r8cPost drops one file and returns its name.
func r8cPost(t *testing.T, rt *Runtime, msg InboxMessage) string {
	t.Helper()
	if err := PostInbox(rt.sess.Path, msg); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(InboxDir(rt.sess.Path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("inbox: %v %v", entries, err)
	}
	return entries[0].Name()
}

// r8cLock makes the inbox directory unwritable, so a removal fails.
func r8cLock(t *testing.T, rt *Runtime) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("runs as root: an unwritable directory does not stop root from removing the file")
	}
	dir := InboxDir(rt.sess.Path)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
}

// r8cWords lists, for each event of type typ whose text is text, the
// inbox file it names.
func r8cWords(rt *Runtime, typ, text string) []string {
	var got []string
	for _, ev := range rt.st.Events {
		if ev.Type != typ {
			continue
		}
		var d struct {
			Text  string `json:"text"`
			Inbox string `json:"inbox"`
		}
		if ev.Decode(&d) == nil && d.Text == text {
			got = append(got, d.Inbox)
		}
	}
	return got
}

func r8cOne(t *testing.T, what string, got []string, name string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("%s: %d of them (%q), want 1", what, len(got), got)
	}
	if got[0] != name {
		t.Fatalf("%s names %q, want %q", what, got[0], name)
	}
}

// (a) The removal fails: two polls take the file once.
func TestR8InboxRemovalFailsTakesOnce(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	name := r8cPost(t, rt, InboxMessage{Type: "message", Text: "r8c hello", From: "web"})
	r8cLock(t, rt)
	rt.pollInbox()
	rt.pollInbox()
	r8cOne(t, "user.message", r8cWords(rt, event.UserMessage, "r8c hello"), name)
}

// (b) Cut after the write, before the removal: the next run finds the
// file still there and takes nothing more from it, and removes it.
func TestR8InboxLeftAfterItsWriteIsNotTakenAgain(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	name := r8cPost(t, rt, InboxMessage{Type: "message", Text: "r8c again", From: "web"})
	r8cLock(t, rt)
	rt.pollInbox()
	path := rt.sess.Path
	rt.sess.Close()
	os.Chmod(InboxDir(path), 0o755)
	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: rt.opts.Project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.sess.Close()
	if !rt2.sess.Stale[name] {
		t.Fatalf("the file is not stale for the second run: %v", rt2.sess.Stale)
	}
	rt2.pollInbox()
	r8cOne(t, "user.message after the second run", r8cWords(rt2, event.UserMessage, "r8c again"), name)
	if entries, _ := os.ReadDir(InboxDir(path)); len(entries) != 0 {
		t.Fatalf("the file taken already is still in the inbox: %v", entries)
	}
}

// (c) No loss: when the user.message is written, its file is still in the
// inbox, so a cut at any point before that write leaves the file for the
// next run, and one after it leaves (b).
func TestR8InboxMessageIsWrittenBeforeItsFileGoes(t *testing.T) {
	ui := &r8cUI{}
	rt := r8cNew(t, ui)
	name := r8cPost(t, rt, InboxMessage{Type: "message", Text: "r8c kept", From: "web"})
	ui.mu.Lock()
	ui.dir = InboxDir(rt.sess.Path)
	ui.mu.Unlock()
	rt.pollInbox()
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if len(ui.present) != 1 || !ui.present[0] {
		t.Fatalf("file present at the write: %v, want [true]", ui.present)
	}
	r8cOne(t, "user.message", r8cWords(rt, event.UserMessage, "r8c kept"), name)
	if entries, _ := os.ReadDir(ui.dir); len(entries) != 0 {
		t.Fatalf("the file is still in the inbox after the poll: %v", entries)
	}
}

// (d) An answer to the open question: taken once, and the file left behind
// is not turned into a message once the question is closed.
func TestR8InboxAnswerRemovalFailsTakesOnce(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	rt.append(event.New(event.Question, event.ActorEagent, event.QuestionData{ID: "q1", Text: "Merge it?"}))
	name := r8cPost(t, rt, InboxMessage{Type: "answer", QuestionID: "q1", Text: "r8c yes", From: "web"})
	r8cLock(t, rt)
	rt.pollInbox()
	rt.pollInbox()
	if got := r8cWords(rt, event.UserMessage, "r8c yes"); len(got) != 0 {
		t.Fatalf("the answer's file became a user.message too: %q", got)
	}
	r8cOne(t, "user.answer", r8cWords(rt, event.UserAnswer, "r8c yes"), name)
}

// An event that did not come from an inbox file carries no file name.
func TestR8InboxNameOnlyOnInboxWords(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	rt.onInput("r8c typed")
	rt.handleInbox(InboxMessage{Type: "message", Text: "r8c handed", From: "finalechat", PhoneID: "m1"})
	for _, text := range []string{"r8c typed", "r8c handed"} {
		if got := r8cWords(rt, event.UserMessage, text); len(got) != 1 || got[0] != "" {
			t.Fatalf("%s: %q, want one with no file name", text, got)
		}
	}
	for _, ev := range rt.st.Events {
		if ev.Type != event.UserMessage && ev.Type != event.UserAnswer && strings.Contains(string(ev.Data), `"inbox"`) {
			t.Fatalf("%s carries a file name: %s", ev.Type, ev.Data)
		}
	}
}

// A command sent through the inbox leaves no event that names its file, so
// its file goes before it is carried out (found by an independent reader of
// the change above: carried out first, a /quit left behind by a cut was
// carried out again by the resumed run). When the file cannot be removed
// the command is not carried out; the run that can remove it carries it
// out, once.
func TestR8InboxCommandIsCarriedOutOnlyOnceItsFileIsGone(t *testing.T) {
	ui := &fakeUI{}
	rt := r8cNew(t, ui)
	r8cPost(t, rt, InboxMessage{Type: "message", Text: "/quit", From: "web"})
	r8cLock(t, rt)
	rt.pollInbox()
	rt.pollInbox()
	if rt.ending {
		t.Fatalf("/quit was carried out although its file could not be removed: a later run would carry it out again")
	}
	if n := strings.Count(strings.Join(ui.logged(), "\n"), "was not carried out"); n != 1 {
		t.Fatalf("said %d times that it was not carried out, want once: %v", n, ui.logged())
	}
	path := rt.sess.Path
	rt.sess.Close()
	if err := os.Chmod(InboxDir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: rt.opts.Project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.sess.Close()
	rt2.pollInbox()
	if entries, _ := os.ReadDir(InboxDir(path)); !rt2.ending || len(entries) != 0 {
		t.Fatalf("the run that could remove the file: ending %v, %d files left; want it carried out and gone", rt2.ending, len(entries))
	}
}

// What is words and what is a command, a stop or a dismissal.
func TestR8InboxWords(t *testing.T) {
	slash := func(line, _ string) bool { return strings.HasPrefix(line, "/") }
	for _, tc := range []struct {
		msg  InboxMessage
		want bool
	}{
		{InboxMessage{Type: "message", Text: "run the tests"}, true},
		{InboxMessage{Text: "run the tests"}, true},
		{InboxMessage{Type: "answer", Text: "blue"}, true},
		{InboxMessage{Type: "message", Text: "  /quit"}, false},
		{InboxMessage{Type: "stop"}, false},
		{InboxMessage{Type: "dismiss", QuestionID: "q1"}, false},
	} {
		if got := inboxWords(tc.msg, slash); got != tc.want {
			t.Fatalf("%+v: words = %v, want %v", tc.msg, got, tc.want)
		}
	}
}
