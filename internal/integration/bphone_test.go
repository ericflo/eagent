package integration

import (
	"context"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// A run by an eagent without the ledger is cut in the middle of a turn; a
// message is sent from the app; this eagent resumes the session, closing
// the cut turn before its session.resume, and is cut itself before its
// mirror reads the thread. The message waited after the older run's last
// write, so it is the one to resume with, though the resume's own repairs
// were written after it.
func TestAMessageAfterACutOlderRunAndBeforeACutLedgerRunIsFound(t *testing.T) {
	// It fails: ReadPhoneLedger takes Since from the event just before the
	// session.resume that starts the ledger, and that is the resume's own
	// repair (the interrupted turn's end), written after the message. Not
	// fixed here: telling the resume's repairs from the older run's own
	// writes needs a marker the record does not have.
	t.Skip("known loss, not fixed: see the comment above")
	project := t.TempDir()
	base := time.Now().Add(-2 * time.Hour).UTC()
	sid := finishedSession(t, project, base)
	at := func(ev event.Event, d time.Duration) event.Event { ev.Time = base.Add(d); return ev }
	r6Record(t, project, sid,
		at(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{}), time.Minute), // the older eagent
		at(event.New(event.TurnStart, event.ActorEagent, event.TurnData{}), 2*time.Minute),             // cut here
		// this eagent's resume, an hour later: the cut turn is closed first
		at(event.New(event.TurnEnd, event.ActorEagent, event.TurnData{Reason: "interrupted"}), time.Hour),
		at(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{PhoneLedger: true}), time.Hour),
	)
	f := newR6Thread(t)
	f.say(sid, "m0", "old", base.Add(-time.Minute), nil)
	f.say(sid, "m1", "go on", base.Add(30*time.Minute), nil)
	m, found, err := nextPhoneMessage(context.Background(), f.watcher(project, &fakeResumeHost{hosted: map[string]bool{}}, t).client, r6Info(t, project, sid), nil)
	if err != nil || !found || m.ID != "m1" {
		t.Fatalf("next message %q found=%v err=%v, want m1", m.ID, found, err)
	}
}
