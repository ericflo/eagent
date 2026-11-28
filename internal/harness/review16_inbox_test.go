package harness

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A stop is in a running session's inbox, not yet read, when the web
// server hands the session something more (Take). The stop was written
// first and is read first: the run begins to end, and Take says it took
// nothing, so the server resumes the session with it afterwards. Take
// used to write its file behind the stop and read both: words were
// recorded into the run that was quitting and reported delivered, and a
// command or a dismissal came back as an error.
func TestTakeReadsWhatWaitsInTheInboxFirst(t *testing.T) {
	for _, msg := range []InboxMessage{
		{Type: "message", Text: "one more thing", From: "web"},
		{Type: "message", Text: "/restart", From: "web"},
		{Type: "dismiss", QuestionID: "q1", From: "web"},
	} {
		t.Run(msg.Type+" "+msg.Text, func(t *testing.T) {
			t.Setenv("EAGENT_TEST_KEY", "x")
			ui := &fakeUI{input: make(chan string)}
			rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Interactive: true}, ui)
			if err != nil {
				t.Fatal(err)
			}
			ran := make(chan struct{})
			go func() { rt.Run(context.Background()); close(ran) }()
			rt.sync(func() {
				if err := PostInbox(rt.sess.Path, InboxMessage{Type: "stop", From: "web"}); err != nil {
					t.Error(err)
				}
			})
			took, err := rt.Take(msg)
			<-ran
			if took || err != nil {
				t.Fatalf("Take behind a stop that was not read yet: took %v, error %v; want nothing taken and no error (the run is ending)", took, err)
			}
			if left, _ := os.ReadDir(InboxDir(rt.sess.Path)); len(left) != 0 {
				t.Fatalf("%d file(s) left in the inbox", len(left))
			}
		})
	}
}

// A run that is ending leaves a dismissal written by hand (a file with no
// id of its own) in the inbox for the next run, and the message written
// after it. The file keeps its name, and so its place before the message:
// it used to be renamed to the present time first, which put it after the
// message, and the next run took the message as the answer to the
// question the dismissal was to close.
func TestAFileLeftForTheNextRunKeepsItsPlaceInTheInbox(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Interactive: true}, &fakeUI{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	dir := InboxDir(rt.sess.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01-dismiss.json"), []byte(`{"type":"dismiss","question_id":"q1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "/restart", From: "web"}); err != nil {
		t.Fatal(err)
	}
	rt.ending = true
	rt.pollInbox()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "01-dismiss.json" {
		t.Fatalf("the inbox holds %s; want the dismissal under its own name, before the message", strings.Join(names, ", "))
	}
}
