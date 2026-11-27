package harness

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// k5Words are the user messages of the record that hold text.
func k5Words(g *phoneRig, text string) int {
	n := 0
	for _, ev := range g.log() {
		var d event.UserMessageData
		if ev.Type == event.UserMessage && ev.Decode(&d) == nil && d.Text == text {
			n++
		}
	}
	return n
}

// k5Inbox is how many files wait in the session's inbox.
func k5Inbox(g *phoneRig) int {
	entries, _ := os.ReadDir(InboxDir(g.rt.sess.Path))
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

// k5Ending is a session with no phone whose closing word is still being
// written when the test returns: its run has begun to end and waits for
// that word (finish's grace), and has looked at its inbox for the last
// time in its loop.
func k5Ending(t *testing.T) *phoneRig {
	t.Helper()
	setFor(t, &finalReportGrace, 300*time.Millisecond)
	writing := make(chan struct{})
	var once sync.Once
	never := make(chan struct{})
	g := phoneStart(t, nil, "merge the PRs", false, func(_ string, msgs []map[string]any) reply {
		switch {
		case phoneLastIsTool(msgs):
			return hold()
		case phoneEnding(msgs):
			once.Do(func() { close(writing) })
			r := say("Merged both.")
			r.block = never
			return r
		case strings.Contains(lastUserText(msgs), "finished a turn"):
			return stopsWhenDone()
		}
		return hold()
	}, nil)
	t.Cleanup(func() { close(never) }) // before the model server is closed
	select {
	case <-writing:
	case <-time.After(15 * time.Second):
		t.Fatal("no closing word")
	}
	g.cancel() // as Ctrl-C: the session ends, and waits for the closing word
	waitFor(t, "the session to be ending", func() bool {
		ending := false
		g.rt.sync(func() { ending = g.rt.ending })
		return ending
	})
	return g
}

// D3, a session a terminal runs: words written into its inbox (by the web
// UI, say) while the run is ending, after its loop's last look, are read by
// the run's last step: they are on the record, kept for the next run, and
// no file is left.
func TestK5WordsWrittenAsTheRunEndsAreReadByItsLastStep(t *testing.T) {
	g := k5Ending(t)
	if err := PostInbox(g.rt.sess.Path, InboxMessage{Type: "message", Text: "and bump the version", From: "web"}); err != nil {
		t.Fatal(err)
	}
	g.exit(10 * time.Second)
	if n := k5Words(g, "and bump the version"); n != 1 {
		t.Fatalf("the words are on the record %d times, want once", n)
	}
	if n := k5Inbox(g); n != 0 {
		t.Fatalf("%d files are left in the inbox", n)
	}
}

// D3, the gap after that last step: words written once the run has read
// its inbox for the last time, until it lets the session go, wait in the
// inbox, and the next run takes them, once.
func TestK5WordsWrittenAfterTheLastStepAreTakenByTheNextRun(t *testing.T) {
	g := k5Ending(t)
	g.exit(10 * time.Second)
	if err := PostInbox(g.rt.sess.Path, InboxMessage{Type: "message", Text: "and bump the version", From: "web"}); err != nil {
		t.Fatal(err)
	}
	g.resume(Options{Interactive: true})
	waitFor(t, "the next run to take the words", func() bool { return k5Words(g, "and bump the version") == 1 })
	g.rt.sync(func() {})
	if n := k5Words(g, "and bump the version"); n != 1 {
		t.Fatalf("the words are on the record %d times, want once", n)
	}
	if n := k5Inbox(g); n != 0 {
		t.Fatalf("%d files are left in the inbox", n)
	}
}
