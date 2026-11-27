package harness

import (
	"os"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// A command written into the inbox of a run that is ending (by the web
// page, for a session a terminal runs: the page answers "delivered") is
// taken by the run's last look (finish's pollInbox, 448720a): it is
// recorded as taken (inbox.taken) and its file removed, but the loop is
// gone, so the command is never carried out and nobody is told. On main
// the run's end did not read the inbox: the file waited, and the next run
// carried the command out.
func TestSR2WebACommandWrittenAsTheRunEndsIsNeitherCarriedOutNorLeft(t *testing.T) {
	g := k5Ending(t)
	if err := PostInbox(g.rt.sess.Path, InboxMessage{Type: "message", Text: "/restart", From: "web"}); err != nil {
		t.Fatal(err)
	}
	g.exit(10 * time.Second)
	evs := g.log()
	taken := -1
	for i, ev := range evs {
		var d map[string]string
		if ev.Type == inboxTakenEvent && ev.Decode(&d) == nil && d["text"] == "/restart" {
			taken = i
		}
	}
	left := k5Inbox(g)
	if taken < 0 {
		// Not taken by the ending run: its file waits for the next run,
		// which carries the command out, as on main.
		if left != 1 {
			t.Fatalf("the command was not taken and %d file(s) are left in the inbox, want its one", left)
		}
		g.resume(Options{Interactive: true})
		waitFor(t, "the next run to take the command", func() bool {
			for _, ev := range g.log() {
				var d map[string]string
				if ev.Type == inboxTakenEvent && ev.Decode(&d) == nil && d["text"] == "/restart" {
					return true
				}
			}
			return false
		})
		return
	}
	var after []string
	for _, ev := range evs[taken+1:] {
		switch ev.Type {
		case event.RelayStart, event.RelayState, event.RelayEnd, event.Message, event.HarnessMessage:
			after = append(after, ev.Type)
		}
	}
	if left == 0 && len(after) == 0 {
		t.Fatalf("the /restart the web page was told was delivered is on the record as taken (inbox.taken, seq %d), was never carried out or answered (no relay event or message after it), and its file is gone, so no later run will carry it out; events after it: %v", evs[taken].Seq, sr2Types(evs[taken+1:]))
	}
}

func sr2Types(evs []event.Event) []string {
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

// The sibling in the loop's own looks: a stop and a /restart in the same
// look. The /restart is not taken by the run the stop ends (its work would
// fail on the ending session, or its reply be dropped once the loop has
// closed): it is not recorded as taken, its file stays, and the next run
// takes it.
func TestK7ACommandAfterAStopInTheSameLookIsLeftForTheNextRun(t *testing.T) {
	rt := r8cNew(t, &fakeUI{})
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "stop", From: "web"}); err != nil {
		t.Fatal(err)
	}
	if err := PostInbox(rt.sess.Path, InboxMessage{Type: "message", Text: "/restart", From: "web"}); err != nil {
		t.Fatal(err)
	}
	rt.pollInbox()
	takenBy := func(rt *Runtime) bool {
		for _, ev := range rt.st.Events {
			var d map[string]string
			if ev.Type == inboxTakenEvent && ev.Decode(&d) == nil && d["text"] == "/restart" {
				return true
			}
		}
		return false
	}
	if !rt.ending || takenBy(rt) {
		t.Fatalf("ending %v, /restart taken %v; want the stop, and /restart left", rt.ending, takenBy(rt))
	}
	if entries, _ := os.ReadDir(InboxDir(rt.sess.Path)); len(entries) != 1 {
		t.Fatalf("files left: %v, want the /restart", entries)
	}
	rt2 := k1Next(t, rt, &fakeUI{})
	rt2.pollInbox()
	if !takenBy(rt2) {
		t.Fatal("the next run did not take the /restart")
	}
}
