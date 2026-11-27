package web

import (
	"slices"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/integration"
	"github.com/ericflo/eagent/internal/store"
)

// k12Marks marks the sessions' stops, each made at at.
func k12Marks(at time.Time, ids ...string) []stopMark {
	var out []stopMark
	for _, id := range ids {
		out = append(out, stopMark{Session: id, At: at})
	}
	return out
}

// A mark left on the list (a stop the start could not finish) survives
// this server's own clean shutdown: the list it writes keeps every mark
// already there, with the time it was made, beside the sessions it hosts.
// It used to be overwritten.
func TestK12AMarkOnTheListSurvivesACleanShutdown(t *testing.T) {
	s, project := newTestServer(t)
	marked := srAgentSession(t, project)
	at := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	s.mu.Lock()
	s.writeHostedList([]string{marked.ID}, at, k12Marks(at, marked.ID))
	s.running["other"] = fakeHosted() // a session this server hosts
	s.mu.Unlock()
	t.Cleanup(func() { s.mu.Lock(); delete(s.running, "other"); s.mu.Unlock() })
	s.rememberHosted() // this server's shutdown begins
	s.mu.Lock()
	list, ok := s.readHosted()
	s.mu.Unlock()
	mark, kept := list.mark(marked.ID)
	// (Changed with the sixth review: a marked session is only on the
	// marks, never among the sessions to bring back.)
	if !ok || slices.Contains(list.Sessions, marked.ID) || !slices.Contains(list.Sessions, "other") || !kept || !mark.At.Equal(at) {
		t.Fatalf("the list the shutdown wrote: %+v (%v), want the mark kept, as made at %s, beside the hosted session", list, ok, at)
	}
}

// A mark kept so keeps its own time: a run of the session begun after the
// mark, but before the list was written again, still makes it no longer
// hold at the next start.
func TestK12AKeptMarkIsJudgedByItsOwnTime(t *testing.T) {
	s, project := newTestServer(t)
	kills := make(chan string, 8)
	old := killAgent
	killAgent = func(host, target string) error { kills <- host + " " + target; return nil }
	t.Cleanup(func() { killAgent = old })
	info := srAgentSession(t, project)
	at := time.Now().Add(-time.Hour).UTC()
	s.mu.Lock()
	s.writeHostedList([]string{info.ID}, at, k12Marks(at, info.ID))
	s.mu.Unlock()
	// The session runs since (its record: a session.resume after the mark).
	sess, _, err := store.Open(info.Path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Append(event.New(event.SessionResume, event.ActorHarness, event.SessionResumeData{Interactive: true})); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	s.rememberHosted() // the list is written again, the mark kept
	oldRestore := restoreSession
	restoreSession = func(*Server, string, integration.PhoneMessage) error { return nil }
	t.Cleanup(func() { restoreSession = oldRestore })
	New(project, nil).restoreHosted(t.Context())
	if len(kills) != 0 {
		t.Fatalf("a mark older than a run since killed %q", <-kills)
	}
}
