package relay

// The sixteenth review's seat findings (sr2seat).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Antigravity writes no final step for an interrupted turn: its poll reads
// the status file (idle) and folds a turn end of its own, and the quota
// there as usage news. Those observations come from no transcript line, so
// they start at position 0. A reattached seat whose record holds a read
// mark takes every observation that starts before the mark for history, so
// both are dropped: the turn is never reported, the seat says working
// until it goes quiet for ten minutes, and the usage news is never given.
// On main (no read mark) the same observations, stamped now, are news.
func TestSr2SeatAgyStatusTurnEndAndQuotaAfterAReattachWithAReadMark(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	dir := t.TempDir()
	status := filepath.Join(dir, "agy-status.json")
	transcript := filepath.Join(dir, "transcript_full.jsonl")
	// The history the record has read: one step, ending at 4097.
	if err := os.WriteFile(transcript, []byte(`{"type":"USER_INPUT"`+strings.Repeat(" ", 4096-20)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeStatus := func(state string) {
		t.Helper()
		raw := `{"conversation_id":"` + id + `","agent_state":"` + state + `","model":{"display_name":"Gemini 3.8"},"quota":{"gemini-5h":{"remaining_fraction":0.01,"reset_time":"2026-11-28T00:00:00Z"}}}`
		if err := os.WriteFile(status, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name string
		read int64
	}{{"no read mark on the record (as on main)", 0}, {"the record's read mark", 4097}} {
		t.Run(c.name, func(t *testing.T) {
			writeStatus("running")
			s := attachSeat(Spec{StatusFile: status, Read: c.read}, antigravityAgent{}, &fakeHost{alive: true}, "x", id, transcript, time.Now().Add(-time.Hour))
			s.caughtUp(time.Now()) // the transcript held nothing new
			// News after the reattach, past the mark: a prompt starts a turn.
			now := time.Now()
			s.fold(obs{kind: "user", text: "refactor the parser", at: now, pos: 4097})
			s.fold(obs{kind: "working", at: now, pos: 4097})
			drain(s.events)
			// The user interrupts it: agy writes no last step and says idle.
			writeStatus("idle")
			s.mu.Lock()
			s.lastLine = time.Now().Add(-time.Minute)
			s.mu.Unlock()
			antigravityAgent{}.poll(s, time.Now())
			var turn, idle, limit bool
			for _, ev := range drain(s.events) {
				switch {
				case ev.Kind == KindTurn && ev.Interrupted:
					turn = true
				case ev.Kind == KindState && ev.State == "idle":
					idle = true
				case ev.Kind == KindLimit:
					limit = true
				}
			}
			s.mu.Lock()
			state := s.state
			s.mu.Unlock()
			if !turn || !idle || state != "idle" {
				t.Errorf("after the interrupt: turn reported %v, idle reported %v, seat state %q; want the interrupted turn reported and the seat idle", turn, idle, state)
			}
			if !limit {
				t.Errorf("the status file's quota (99%% used) was never reported")
			}
		})
	}
}

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
