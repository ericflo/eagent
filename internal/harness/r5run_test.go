package harness

import (
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// A run that resumes a session while the last run is still writing its
// final events waits for the lock (store's short retry) and must then read
// the record as it stands after that run let go: what was committed in the
// wait is part of the history, and the new run's events follow it in
// order, one number after another.
func TestResumingWhileTheLastRunFinishesWritingKeepsTheRecordInOrder(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()

	cfg := testConfig(s.model.srv.URL)
	resume := func() (*Runtime, error) {
		return Resume(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 1)}, path)
	}
	// The last run: it holds the lock and has more to write.
	last, err := resume()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		rt  *Runtime
		err error
	}
	next := make(chan result, 1)
	go func() {
		rt, err := resume()
		next <- result{rt, err}
	}()
	// Well inside the lock's retry window, the last run commits its final
	// events and lets go.
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if _, err := last.sess.Append(event.New(event.Message, event.ActorEagent, event.MessageData{Text: "written while the next run waited"})); err != nil {
			t.Fatal(err)
		}
	}
	lastSeq := last.sess.LastSeq()
	last.sess.Close()

	got := <-next
	if got.err != nil {
		t.Fatalf("the next run could not take the session: %v", got.err)
	}
	defer got.rt.sess.Close()
	ev, err := got.rt.sess.Append(event.New(event.Message, event.ActorEagent, event.MessageData{Text: "the next run"}))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Seq <= lastSeq {
		t.Errorf("the next run numbered its event %d, at or below the last run's %d", ev.Seq, lastSeq)
	}
	if n := got.rt.st.LastSeq(); n < lastSeq {
		t.Errorf("the next run's state ends at %d and misses what was committed up to %d", n, lastSeq)
	}
	events, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d of the record (%s, %s:%d) is numbered %d", i+1, e.Type, e.Source.File, e.Source.Line, e.Seq)
		}
	}
}
