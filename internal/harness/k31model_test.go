package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/llm"
)

// A call is out when eagent's calls move to another model (a pick in the
// settings), and its failure comes back after the move: it was shown the
// record before the move, and is the old model's. Nothing is counted
// against the model in use: no failure, no wait before the next look, and
// no refusal as too long, which would restart the conversation the new
// model has not been shown. (The route was checked when the call came back
// and the failure taken in a step later; a pick between the two charged
// the new model, and a larger one restarted from a dossier for nothing.)
// A failure of a call shown the record with the move in it is counted.
func TestK31AFailureFromBeforeTheModelChangedIsNotTheNewModels(t *testing.T) {
	idle := func(string, []map[string]any) reply { return hold() }
	s := startSession(t, "", idle, nil)
	s.settled()
	var shown int64
	s.rt.sync(func() { shown = s.rt.st.LastSeq() })
	r5Swap(t, s, 200_000, idle)
	tooLong := &llm.APIError{Status: 400, Body: `{"error":{"message":"prompt is too long for the model's context window"}}`}
	errs := len(s.events(event.Error))
	s.rt.sync(func() {
		tr := &s.rt.turns
		if p := s.rt.turnFailed(tooLong, shown, false, wake{kind: wakeUser, event: true}); !p.waits {
			t.Errorf("the look is not owed again: %+v", p)
		}
		if tr.overflowed || tr.failures != 0 || !tr.retryAt.IsZero() || s.rt.needsDossier() {
			t.Errorf("the old model's refusal was counted against the new one: overflowed %v, %d failures, retry at %v, restart %v", tr.overflowed, tr.failures, tr.retryAt, s.rt.needsDossier())
		}
		s.rt.turnFailed(tooLong, s.rt.st.LastSeq(), false, wake{kind: wakeUser, event: true})
		if !tr.overflowed {
			t.Error("a refusal of a call shown the record after the move was not counted")
		}
	})
	if n := len(s.events(event.Error)); n != errs {
		t.Errorf("%d errors were recorded for the old model's call; the second refusal asks for a restart and records none", n-errs)
	}
}
