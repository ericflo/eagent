package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// A message for eagent that is owed a reminder, and is then answered in a
// turn the session is stopped in the middle of (the model sent its answer
// and looked at something, and the session stopped during the look's
// follow-up call), is no longer owed anything. The record still says it
// is owed (answer.owed written with started=true before the reminder call,
// never cleared because the turn ended "interrupted"), so the resumed
// session tells the model the user "has had no answer" and calls it again
// for a message it already answered.
func TestAnAnswerSentInATurnCutOffByAStopIsNotOwedAfterResume(t *testing.T) {
	old := owedRetryDelays
	owedRetryDelays = []time.Duration{30 * time.Millisecond, time.Hour, time.Hour}
	defer func() { owedRetryDelays = old }()
	var phase atomic.Int32 // 0: first run, 1: answered, 2: resumed
	var falseReminders atomic.Int32
	blocked := make(chan struct{}, 1)
	never := make(chan struct{})
	defer close(never)
	brain := func(_ string, msgs []map[string]any) reply {
		last := lastUserText(msgs)
		switch phase.Load() {
		case 0:
			if strings.Contains(last, owedPhrase) && msgs[len(msgs)-1]["role"] != "tool" {
				phase.Store(1)
				r := say("Codex is idle.")
				r.calls = append(r.calls, tc("session_search", `{"query":"Codex"}`))
				return r
			}
		case 1:
			// The follow-up call after the look: the session is stopped
			// while it is out.
			select {
			case blocked <- struct{}{}:
			default:
			}
			return reply{block: never, calls: hold().calls}
		case 2:
			if strings.Contains(last, owedPhrase) {
				falseReminders.Add(1)
			}
		}
		return hold()
	}
	s := startSession(t, "", brain, func(cfg *config.Config) { cfg.MaxCallsPerTurn = 3 })
	s.settled()
	s.type_("@e what is Codex up to?")
	select {
	case <-blocked:
	case <-time.After(15 * time.Second):
		t.Fatal("the reminder was never answered")
	}
	if s.ui.messageCount() != 1 {
		t.Fatalf("the answer was not sent: %d messages", s.ui.messageCount())
	}
	path, project := s.rt.sess.Path, s.rt.opts.Project
	s.stop()

	phase.Store(2)
	owedRetryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	s2 := resumeOneCall(t, path, project, s.model)
	s2.settled()
	time.Sleep(15 * owedRetryDelays[0]) // a false reminder comes one delay after the resume
	s2.settled()
	var owed int64
	s2.rt.sync(func() { owed = s2.rt.turns.owed })
	var owedRecs []string
	for _, ev := range s2.events(event.Owed) {
		owedRecs = append(owedRecs, string(ev.Data))
	}
	if n := falseReminders.Load(); n > 0 || owed != 0 {
		t.Fatalf("after resume, a message answered before the stop was still owed (owed=%d) and the model was told %d time(s) the user had had no answer; records: %v", owed, n, owedRecs)
	}
}
