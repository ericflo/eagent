package relay

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
)

// pasteFailHost is a terminal whose paste fails.
type pasteFailHost struct{ *fakeHost }

func (h pasteFailHost) Paste(string, string) error { return errors.New("paste-buffer: no server") }

func lostReports(s *Seat) []Event {
	var out []Event
	for _, ev := range drain(s.events) {
		if ev.Kind == KindDelivered && !ev.OK {
			out = append(out, ev)
		}
	}
	return out
}

// A send that fails while its text is typed may have left it on the
// agent's screen, and says so (TypingError); one that fails before typing
// does not.
func TestSendSaysWhetherItFailedWhileTyping(t *testing.T) {
	s := newSeat(Spec{Name: "codex", Profile: config.RelayProfile{Agent: "codex"}}, codexAgent{}, pasteFailHost{&fakeHost{alive: true}}, "x", "")
	s.state = "idle"
	_, err := s.Send("r1", "check the version", "queue")
	var te *TypingError
	if !errors.As(err, &te) || err.Error() != "paste-buffer: no server" {
		t.Errorf("a failed paste: %v", err)
	}
	s.state = "exited"
	if _, err := s.Send("r2", "check the version", "queue"); err == nil || errors.As(err, &te) {
		t.Errorf("an agent that has exited: %v", err)
	}
}

// A message held for the end of a turn and taken back unread certainly did
// not go.
func TestALeftOverHeldMessageIsReportedNotSent(t *testing.T) {
	saved := heldGrace
	heldGrace = 0
	t.Cleanup(func() { heldGrace = saved })
	spool := filepath.Join(t.TempDir(), "after_turn")
	s := newSeat(Spec{Name: "claude", Spool: spool, Hook: "eagent relay-hook"}, claudeAgent{}, &fakeHost{alive: true, screen: claudePromptScreen}, "x", "")
	s.state = "working"
	if _, err := s.Send("r1", "then run the tests", "after_turn"); err != nil {
		t.Fatal(err)
	}
	s.fold(obs{kind: "aborted"})
	drain(s.events)
	s.reportLeftOvers(time.Now().Add(time.Second))
	if lost := lostReports(s); len(lost) != 1 || lost[0].Outcome != OutcomeNotSent {
		t.Errorf("reports %+v", lost)
	}
}

// A typed message the transcript never showed may have arrived.
func TestATypedMessageNeverSeenIsReportedUnconfirmed(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	h := &fakeHost{alive: true}
	s := newSeat(Spec{Name: "codex", Profile: config.RelayProfile{Agent: "codex"}}, codexAgent{}, h, "x", "")
	s.state = "idle"
	if _, err := s.Send("r1", "check the version", "queue"); err != nil {
		t.Fatal(err)
	}
	drain(s.events)
	now := time.Now().Add(time.Minute)
	s.mu.Lock()
	s.stateAt = now.Add(-time.Hour)
	s.mu.Unlock()
	s.checkScreen(now) // the look before: nothing is decided on one
	s.checkScreen(now)
	if lost := lostReports(s); len(lost) != 1 || lost[0].SendID != "r1" || lost[0].Outcome != OutcomeUnconfirmed {
		t.Errorf("reports %+v", lost)
	}
}
