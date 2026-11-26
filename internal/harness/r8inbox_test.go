package harness

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// An inbox file that gives its own name as its id (PostInbox's do) is taken
// exactly once: the user.message or user.answer it becomes is written
// before the file is removed and carries the id, so a removal that fails
// (or a process cut before it) leaves a file a later poll or run recognises
// as already taken. Any other file is removed and then taken, as inbox
// files were before.

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
	// The id is the file's name without ".json".
	if want := strings.TrimSuffix(name, ".json"); got[0] != want {
		t.Fatalf("%s carries %q, want %q", what, got[0], want)
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

// An event that did not come from an inbox file carries no inbox id.
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

// r8cDrop writes an inbox file by hand, as a script does: body is its JSON,
// under a name of the writer's choosing.
func r8cDrop(t *testing.T, sessPath, name, body string) {
	t.Helper()
	dir := InboxDir(sessPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// r8cNote is a message with no id, as a script writes it.
func r8cNote(text string) string {
	return `{"type":"message","text":"` + text + `","from":"script"}`
}

// A script that writes each of its messages under the same name (found by
// a third reader, when a file was known by its name): each is taken, and
// none carries an inbox id.
func TestR8InboxAFileWithNoIDUnderAReusedNameIsTaken(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	for _, text := range []string{"r8c first", "r8c second"} {
		r8cDrop(t, rt.sess.Path, "note.json", r8cNote(text))
		rt.pollInbox()
		if got := r8cWords(rt, event.UserMessage, text); len(got) != 1 || got[0] != "" {
			t.Fatalf("%s: %q, want one with no inbox id", text, got)
		}
	}
}

// The same when the second is written while the session is stopped.
func TestR8InboxAFileWithNoIDUnderAReusedNameIsTakenByTheNextRun(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	r8cDrop(t, rt.sess.Path, "note.json", r8cNote("r8c one"))
	rt.pollInbox()
	path := rt.sess.Path
	rt.sess.Close()
	r8cDrop(t, path, "note.json", r8cNote("r8c two"))
	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: rt.opts.Project}, &fakeUI{}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.sess.Close()
	rt2.pollInbox()
	for _, text := range []string{"r8c one", "r8c two"} {
		if got := r8cWords(rt2, event.UserMessage, text); len(got) != 1 {
			t.Fatalf("%s: %d user.message, want 1", text, len(got))
		}
	}
}

// A file with no id is removed once, before it is handled, as before (found
// by a fourth reader, when the poll removed every file again at its end):
// one written under the same name while that one is being handled is left
// for the next poll, which takes it.
func TestR8InboxAFileWithNoIDWrittenWhileTheLastIsHandledIsTaken(t *testing.T) {
	ui := &r8cHelpUI{}
	rt := r8cNew(t, ui)
	ui.dir = InboxDir(rt.sess.Path)
	ui.during = func() { r8cDrop(t, rt.sess.Path, "note.json", r8cNote("r8c during")) }
	r8cDrop(t, rt.sess.Path, "note.json", `{"type":"message","text":"/help"}`)
	rt.pollInbox()
	if len(ui.files) != 1 {
		t.Fatalf("/help was carried out %d times, want once", len(ui.files))
	}
	rt.pollInbox()
	if got := r8cWords(rt, event.UserMessage, "r8c during"); len(got) != 1 {
		t.Fatalf("the message written under the reused name: %d user.message, want 1", len(got))
	}
}

// Only a file that gives its own name as its id has one. Anything else
// under "inbox_id" (the same fourth reader: a number there made the file
// unreadable) is as if the key were not there, which it was not before.
func TestR8InboxOnlyAFileThatGivesItsOwnNameHasAnID(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"20261126T160003.904259023.json", `{"type":"message","text":"x","inbox_id":"20261126T160003.904259023"}`, "20261126T160003.904259023"},
		{"note.json", `{"type":"message","text":"x"}`, ""},
		{"note.json", `{"type":"message","text":"x","inbox_id":""}`, ""},
		{"note.json", `{"type":"message","text":"x","inbox_id":"other"}`, ""},
		{"note.json", `{"type":"message","text":"x","inbox_id":7}`, ""},
		{"note.json", `{"type":"message","text":"x","inbox_id":null}`, ""},
		{"note.json", `not json`, ""},
	} {
		if got := inboxFileID([]byte(tc.raw), tc.name); got != tc.want {
			t.Errorf("%s %s: id %q, want %q", tc.name, tc.raw, got, tc.want)
		}
	}
	rt := r8cNew(t, &fakeUI{})
	for i, body := range []string{
		`{"inbox_id":7,"type":"message","text":"r8c numbered","from":"script"}`,
		`{"inbox_id":"other","type":"message","text":"r8c other","from":"script"}`,
		`{"inbox_id":"other","type":"message","text":"r8c other again","from":"script"}`,
	} {
		r8cDrop(t, rt.sess.Path, "note.json", body)
		rt.pollInbox()
		text := []string{"r8c numbered", "r8c other", "r8c other again"}[i]
		if got := r8cWords(rt, event.UserMessage, text); len(got) != 1 || got[0] != "" {
			t.Fatalf("%s: %q, want one with no inbox id", text, got)
		}
	}
}

// Two submissions never share an id, so a second one is taken even when
// the first one's file is still in the inbox.
func TestR8InboxEachSubmissionIsTaken(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	r8cPost(t, rt, InboxMessage{Type: "message", Text: "r8c a", From: "web"})
	r8cLock(t, rt)
	rt.pollInbox()
	os.Chmod(InboxDir(rt.sess.Path), 0o755)
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "r8c b", From: "web"}); err != nil {
		t.Fatal(err)
	}
	rt.pollInbox()
	for _, text := range []string{"r8c a", "r8c b"} {
		if got := r8cWords(rt, event.UserMessage, text); len(got) != 1 || got[0] == "" {
			t.Fatalf("%s: %q, want one that carries its id", text, got)
		}
	}
	if entries, _ := os.ReadDir(InboxDir(rt.sess.Path)); len(entries) != 0 {
		t.Fatalf("files left in the inbox: %v", entries)
	}
}

// r8cHelpUI notes, each time /help is carried out, how many files were in
// the inbox, and can make the inbox writable again at that moment. (/help
// from a script is eagent's own command; from the web, with an agent
// running, it is typed into the agent.)
type r8cHelpUI struct {
	fakeUI
	dir    string
	unlock bool
	files  []int
	during func() // runs once, while /help is carried out
}

func (u *r8cHelpUI) Log(format string, args ...any) {
	u.fakeUI.Log(format, args...)
	if u.dir != "" && strings.HasPrefix(format, "eagent: /status") {
		entries, _ := os.ReadDir(u.dir)
		u.files = append(u.files, len(entries))
		if u.unlock {
			os.Chmod(u.dir, 0o755)
		}
		if u.during != nil {
			u.during()
			u.during = nil
		}
	}
}

// A command sent through the inbox leaves no event that names its file, so
// its file goes before it is carried out, as it did before the change above
// (an independent reader showed that carried out first, a command left
// behind by a cut was carried out again by the resumed run). It is carried
// out whether or not the removal works, as before; within one run, once.
func TestR8InboxCommandFileGoesBeforeItIsCarriedOut(t *testing.T) {
	ui := &r8cHelpUI{}
	rt := r8cNew(t, ui)
	ui.dir = InboxDir(rt.sess.Path)
	r8cPost(t, rt, InboxMessage{Type: "message", Text: "/help"})
	rt.pollInbox()
	rt.pollInbox()
	if len(ui.files) != 1 || ui.files[0] != 0 {
		t.Fatalf("files in the inbox each time /help was carried out: %v, want once with none", ui.files)
	}
}

func TestR8InboxCommandWhoseFileStaysIsCarriedOutOnceARun(t *testing.T) {
	ui := &r8cHelpUI{}
	rt := r8cNew(t, ui)
	ui.dir = InboxDir(rt.sess.Path)
	r8cPost(t, rt, InboxMessage{Type: "message", Text: "/help"})
	r8cLock(t, rt)
	rt.pollInbox()
	rt.pollInbox()
	rt.pollInbox()
	if len(ui.files) != 1 {
		t.Fatalf("/help was carried out %d times while its file could not be removed, want once", len(ui.files))
	}
}

// A removal that fails once (found by a second reader: the command was then
// neither carried out nor left for later): it is carried out, and the file
// goes when it can.
func TestR8InboxCommandWhoseRemovalFailsOnceIsCarriedOut(t *testing.T) {
	ui := &r8cHelpUI{unlock: true}
	rt := r8cNew(t, ui)
	ui.dir = InboxDir(rt.sess.Path)
	r8cPost(t, rt, InboxMessage{Type: "message", Text: "/help"})
	r8cLock(t, rt)
	rt.pollInbox()
	rt.pollInbox()
	if entries, _ := os.ReadDir(ui.dir); len(ui.files) != 1 || len(entries) != 0 {
		t.Fatalf("/help carried out %d times, %d files left; want once and none", len(ui.files), len(entries))
	}
}

// A stop whose file cannot be removed still stops the session (the next
// run ignores a stop left behind, as before).
func TestR8InboxStopWhoseFileStaysStillStops(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	r8cPost(t, rt, InboxMessage{Type: "stop", From: "web"})
	r8cLock(t, rt)
	rt.pollInbox()
	if !rt.ending {
		t.Fatalf("the stop was not carried out")
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
