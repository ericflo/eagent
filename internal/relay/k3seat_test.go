package relay

// The sixteenth review's seat findings (sr2seat).

import (
	"testing"
)

// sr2PasteGate holds a paste until the test lets it go.
type sr2PasteGate struct {
	fakeHost
	entered, release chan struct{}
}

func (h *sr2PasteGate) Paste(target, text string) error {
	h.entered <- struct{}{}
	<-h.release
	return h.fakeHost.Paste(target, text)
}

// The agent's own command (/compact) is confirmed by Send itself once it
// is typed (new on this branch; main emitted nothing from Send). A seat
// closed while the command is being typed (the session quitting, which
// only stops watching, or a stop or restart, whose Close waits for the
// watcher before the pane is killed) has had its events channel closed by
// the watcher; Send's emitAside then selects between a send on that closed
// channel and the closed stop channel, and Go picks either: half the time
// the process panics ("send on closed channel"), taking eagent (or every
// session of eagent serve) down.
func TestSr2SeatCommandTypedWhileTheSeatClosesDoesNotPanic(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	panics := 0
	const tries = 16
	for i := 0; i < tries; i++ {
		h := &sr2PasteGate{fakeHost: fakeHost{alive: true}, entered: make(chan struct{}), release: make(chan struct{})}
		s := newSeat(Spec{}, claudeAgent{}, h, "sr2close", "")
		s.state = "idle"
		go s.watch()
		got := make(chan any, 1)
		go func() {
			defer func() { got <- recover() }()
			_, _ = s.Send("r1", "/compact", "queue")
		}()
		<-h.entered // past Send's closed check, typing
		s.Close()   // returns once the watcher is done: events is closed
		close(h.release)
		if p := <-got; p != nil {
			panics++
			t.Logf("try %d: Send panicked: %v", i, p)
		}
	}
	if panics > 0 {
		t.Errorf("Send panicked in %d of %d closes during the typing of a command", panics, tries)
	}
}
