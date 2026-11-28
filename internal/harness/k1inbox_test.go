package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// k1Words returns the inbox ids of the user messages on record with these
// words. Loop goroutine (or a runtime whose loop has stopped).
func k1Words(rt *Runtime, text string) []string {
	var ids []string
	for _, ev := range rt.st.Events {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == text {
			ids = append(ids, d.Inbox)
		}
	}
	return ids
}

// D3: words handed to a running session with Take are on its record, with
// their inbox file's id, and the file is gone, when Take returns. Handed
// to a run that has begun to end, nothing is written and Take says so, so
// the caller can resume the session with them without their being taken
// twice.
func TestK1TakeIsTakenAtOnceOrNotAtAll(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	ui := &fakeUI{input: make(chan string)}
	rt, err := New(testConfig("http://127.0.0.1:1"), Options{Project: t.TempDir(), Interactive: true}, ui)
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{})
	go func() { rt.Run(context.Background()); close(ran) }()
	took, err := rt.Take(InboxMessage{Type: "message", Text: "k1 one more thing", From: "web"})
	if !took || err != nil {
		t.Fatalf("a running session's Take: %v, %v", took, err)
	}
	var ids []string
	rt.sync(func() { ids = k1Words(rt, "k1 one more thing") })
	if len(ids) != 1 || ids[0] == "" {
		t.Fatalf("on record with inbox ids %q, want once, with its file's", ids)
	}
	if left, _ := os.ReadDir(InboxDir(rt.sess.Path)); len(left) != 0 {
		t.Fatalf("%d file(s) left in the inbox", len(left))
	}
	rt.post(func() { rt.beginShutdown("quit", 0) })
	took, err = rt.Take(InboxMessage{Type: "message", Text: "k1 and another", From: "web"})
	if took || err != nil {
		t.Fatalf("an ending session's Take: %v, %v", took, err)
	}
	<-ran
	if left, _ := os.ReadDir(InboxDir(rt.sess.Path)); len(left) != 0 {
		t.Fatalf("%d file(s) written to the inbox of a run that was ending", len(left))
	}
	if ids := k1Words(rt, "k1 and another"); len(ids) != 0 {
		t.Fatalf("the ending run took the words %d times", len(ids))
	}
}

// k1Next is the next run of rt's session, with ui; rt's store is closed.
func k1Next(t *testing.T, rt *Runtime, ui UI) *Runtime {
	t.Helper()
	path := rt.sess.Path
	rt.sess.Close()
	rt2, err := Resume(testConfig("http://127.0.0.1:1"), Options{Project: rt.opts.Project}, ui, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt2.sess.Close() })
	return rt2
}

// k1SnapUI keeps, at each user.message written, the inbox's files: what a
// cut just before or just after that write would leave there.
type k1SnapUI struct {
	fakeUI
	dir   string
	snaps []map[string][]byte
}

func (u *k1SnapUI) Trace(ev event.Event) {
	if ev.Type != event.UserMessage || u.dir == "" {
		return
	}
	snap := map[string][]byte{}
	entries, _ := os.ReadDir(u.dir)
	for _, e := range entries {
		if raw, err := os.ReadFile(filepath.Join(u.dir, e.Name())); err == nil {
			snap[e.Name()] = raw
		}
	}
	u.snaps = append(u.snaps, snap)
}

// D2: a file written by hand, with no id of its own, is still in the inbox
// when its words are written, so a cut before that write leaves it for the
// next run; and a cut after it leaves a file the next run knows it took.
// It used to be removed first, and a cut between the two lost the words.
func TestK1InboxHandWrittenWordsSurviveACut(t *testing.T) {
	ui := &k1SnapUI{}
	rt := r8cNew(t, ui)
	ui.dir = InboxDir(rt.sess.Path)
	r8cDrop(t, rt.sess.Path, "note.json", r8cNote("k1 by hand"))
	rt.pollInbox()
	if n := len(r8cWords(rt, event.UserMessage, "k1 by hand")); n != 1 || len(ui.snaps) != 1 {
		t.Fatalf("the words are on record %d times, want once", n)
	}
	if len(ui.snaps[0]) == 0 {
		t.Fatal("the file was removed before its words were written: a cut between the two loses them")
	}
	if left, _ := os.ReadDir(ui.dir); len(left) != 0 {
		t.Fatalf("%d file(s) left in the inbox", len(left))
	}
	// The process is cut just after the write: the inbox is as it was then.
	for name, raw := range ui.snaps[0] {
		if err := os.WriteFile(filepath.Join(ui.dir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rt2 := k1Next(t, rt, &fakeUI{})
	rt2.pollInbox()
	if n := len(r8cWords(rt2, event.UserMessage, "k1 by hand")); n != 1 {
		t.Fatalf("after the cut the words are on record %d times, want once", n)
	}
}

// D1: an eagent command from the inbox whose file cannot be removed is
// carried out once, not once in each run: the next run finds the file
// still there and the record says it was taken.
func TestK1InboxCommandWhoseFileStaysIsNotCarriedOutByTheNextRun(t *testing.T) {
	ui := &r8cHelpUI{}
	rt := r8cNew(t, ui)
	ui.dir = InboxDir(rt.sess.Path)
	r8cPost(t, rt, InboxMessage{Type: "message", Text: "/help"})
	r8cLock(t, rt)
	rt.pollInbox()
	if len(ui.files) != 1 {
		t.Fatalf("/help was carried out %d times, want once", len(ui.files))
	}
	ui2 := &r8cHelpUI{dir: ui.dir}
	rt2 := k1Next(t, rt, ui2)
	rt2.pollInbox()
	if len(ui2.files) != 0 {
		t.Fatal("/help was carried out again by the next run, whose inbox still held its file")
	}
}

// D1, what is left: a command recorded as taken whose run is cut before it
// carries it out is not carried out by the next run, as for a command from
// the phone (recorded as taken before it runs: lost in a kill between the
// two, it can be typed again; run twice, it may not be undone). The next
// run says which command may not have run, naming it from the record
// (inbox.taken's text) or, for a record from before that, its file.
func TestK1InboxCommandCutBeforeItIsCarriedOutIsSaidMayNotHaveRun(t *testing.T) {
	for _, gone := range []bool{false, true} {
		t.Run(map[bool]string{false: "its file still there", true: "its file removed"}[gone], func(t *testing.T) {
			rt := r8cNew(t, &fakeUI{})
			name := r8cPost(t, rt, InboxMessage{Type: "message", Text: "/help"})
			if gone {
				// The run takes the command as it does (inbox.taken, then
				// the file removed): /help records nothing more, so the
				// record ends where a cut just after the removal leaves it.
				rt.pollInbox()
				if last := rt.st.Events[len(rt.st.Events)-1]; last.Type != "inbox.taken" {
					t.Fatalf("the record ends at %s", last.Type)
				}
			} else {
				// A cut just after the record: the file still there, and
				// nothing carried out.
				rt.append(event.New("inbox.taken", event.ActorHarness, map[string]string{"inbox": strings.TrimSuffix(name, ".json")}))
			}
			ui2 := &r8cHelpUI{}
			rt2 := k1Next(t, rt, ui2)
			ui2.dir = InboxDir(rt2.sess.Path)
			rt2.pollInbox()
			if len(ui2.files) != 0 {
				t.Fatalf("/help was carried out %d times by the next run, want none", len(ui2.files))
			}
			said := ""
			for _, ev := range rt2.st.Events {
				var d event.SessionResumeData
				if ev.Type == event.SessionResume && ev.Decode(&d) == nil {
					said = strings.Join(d.Closed, "; ")
				}
			}
			if !strings.Contains(said, "may not have run") || !strings.Contains(said, `"/help"`) {
				t.Fatalf("the resume says %q", said)
			}
		})
	}
}
