package integration

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Finding 6 of the eighth review: 101 messages sent from the app while the
// session was stopped, and no cursor to read on from. The service lists a
// thread's newest hundred when it is asked with no cursor; the session is
// still resumed with the earliest of the 101, so its poll, reading on from
// there, takes them all.
func TestTheSweepResumesFromTheEarliestOfALongBacklog(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "old", "from before the session stopped", stop.Add(-time.Minute), nil)
	for i := 1; i <= 101; i++ {
		f.say(sid, fmt.Sprintf("m%03d", i), fmt.Sprintf("note %d", i), stop.Add(time.Duration(i)*time.Second), nil)
	}
	host := &fakeResumeHost{hosted: map[string]bool{}}
	f.watcher(project, host, t).sweep(context.Background())
	if host.calls() != 1 || host.resumed[0].ID != "m001" {
		t.Fatalf("resumed with %#v, want the earliest (m001)", host.resumed)
	}
}

// The message listed before the earliest that waited is the one a resumed
// session reads on after: the one before it in the thread, also when it
// is older than everything the search listed.
func TestTheMessageBeforeTheEarliestThatWaitedIsFound(t *testing.T) {
	project := t.TempDir()
	sid := finishedSession(t, project, time.Now().Add(-time.Hour))
	f := newR6Thread(t)
	stop := r6Info(t, project, sid).Modified
	f.say(sid, "older", "long before", stop.Add(-2*time.Minute), nil)
	f.say(sid, "old", "from before the session stopped", stop.Add(-time.Minute), nil)
	for i := 1; i <= 101; i++ {
		f.say(sid, fmt.Sprintf("m%03d", i), fmt.Sprintf("note %d", i), stop.Add(time.Duration(i)*time.Second), nil)
	}
	client := f.watcher(project, &fakeResumeHost{hosted: map[string]bool{}}, t).client
	m, prev, found, err := PhoneLedger{}.Waiting(context.Background(), client, "eagent:"+sid, stop)
	if err != nil || !found || m.ID != "m001" || prev != "old" {
		t.Fatalf("waiting = %q after %q (found %v, err %v), want m001 after old", m.ID, prev, found, err)
	}
	// Nothing before it in the thread: the poll then reads from the start.
	f.mu.Lock()
	f.threads[sid] = f.threads[sid][2:]
	f.mu.Unlock()
	if m, prev, found, err = (PhoneLedger{}).Waiting(context.Background(), client, "eagent:"+sid, stop); err != nil || !found || m.ID != "m001" || prev != "" {
		t.Fatalf("waiting = %q after %q (found %v, err %v), want m001 after nothing", m.ID, prev, found, err)
	}
}
