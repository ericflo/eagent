package harness

import (
	"fmt"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// allReachableFromTheNewestDossier checks that every user message before the newest
// dossier's covers is reached from that dossier's text alone, following
// its pointers with session_read (r4Follow).
func allReachableFromTheNewestDossier(t *testing.T, rt *Runtime) {
	t.Helper()
	var d event.DossierData
	for _, ev := range rt.st.Events {
		if ev.Type == event.Dossier {
			_ = ev.Decode(&d)
		}
	}
	if r4Pointer.FindStringSubmatch(d.Text) == nil {
		t.Fatal("the newest dossier points at no earlier briefing: the chain is not exercised")
	}
	missing := 0
	for _, ev := range rt.st.Events {
		if ev.Type != event.UserMessage || ev.Seq > d.CoversSeq {
			continue
		}
		if _, ok := r4Follow(t, rt, d.Text, ev.Source.File, ev.Source.Line); !ok {
			if missing < 5 {
				t.Errorf("user message at %s is not reached from the newest dossier", ev.Source)
			}
			missing++
		}
	}
	if missing > 0 {
		t.Fatalf("%d user messages are not reached", missing)
	}
}

// A chain with an unfinished restart in the middle (the run stopped after
// the new file's subsession.start; the next run wrote messages there, then
// finished the restart), a briefing the harness wrote, and a briefing that
// carried what had not been seen (covers before the end of the file).
// Every message the user sent is reached from the newest dossier.
func TestTheChainOfBriefingsReachesEveryMessageThroughOddLinks(t *testing.T) {
	rt := r4SmallRuntime(t)
	defer rt.sess.Close()
	at := time.Now().Add(-1000 * time.Hour)
	add := func(text string) {
		at = at.Add(time.Minute)
		ev := event.New(event.UserMessage, event.ActorUser, event.UserMessageData{Text: text})
		ev.Time = at
		rt.append(ev)
	}
	r4Restarts(t, rt, 12, &at)
	// An unfinished restart: end and start written, no dossier.
	if _, err := rt.openSubsession("context full", "rollover", 12000); err != nil {
		t.Fatal(err)
	}
	if !rt.st.Unfinished() {
		t.Fatal("not unfinished")
	}
	for i := 0; i < 4; i++ {
		add(fmt.Sprintf("said in the unfinished file %d", i))
	}
	rt.finishDossier(dossierJob{covers: rt.st.LastSeq(), lines: rt.recordLines()}, dossierResult{ok: true, text: briefing([]string{rt.sess.Current() + ":3", rt.sess.Current() + ":4", rt.sess.Current() + ":5"})})
	r4Restarts(t, rt, 6, &at)
	// A harness-written one.
	for i := 0; i < 5; i++ {
		add(fmt.Sprintf("before the harness briefing %d", i))
	}
	rt.finishDossier(dossierJob{covers: rt.st.LastSeq(), lines: rt.recordLines()}, dossierResult{})
	r4Restarts(t, rt, 6, &at)
	// One that covers only part of its file: the rest is carried over.
	for i := 0; i < 6; i++ {
		add(fmt.Sprintf("seen before the carried restart %d", i))
	}
	covers := rt.st.LastSeq()
	for i := 0; i < 3; i++ {
		add(fmt.Sprintf("unseen, carried over %d", i))
	}
	rt.finishDossier(dossierJob{covers: covers, lines: rt.recordLines()}, dossierResult{ok: true, text: briefing([]string{rt.sess.Current() + ":3", rt.sess.Current() + ":4", rt.sess.Current() + ":5"})})
	r4Restarts(t, rt, 8, &at)
	allReachableFromTheNewestDossier(t, rt)
}
