package relay

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// handOverFails is an agent whose after-turn hand-over fails, while the
// watcher reads an earlier user entry that holds the send's words. Codex's
// own queue is not run (no real agent program): it fails as `codex queue`
// exiting 1 would.
type handOverFails struct {
	Agent
	entry string
}

func (a handOverFails) afterTurn(s *Seat, id, text string) error {
	s.fold(obs{kind: "user", text: a.entry})
	if _, codex := a.Agent.(codexAgent); codex {
		return errors.New("codex queue: exit status 1")
	}
	return a.Agent.afterTurn(s, id, text)
}

// handOverWorks is Codex whose queue takes the message (not run).
type handOverWorks struct{ codexAgent }

func (handOverWorks) afterTurn(*Seat, string, string) error { return nil }

// pasteFailsAsAnEntryIsRead fails the paste (nothing pasted) while the
// watcher reads an earlier user entry that holds the send's words.
type pasteFailsAsAnEntryIsRead struct {
	*fakeHost
	seat  *Seat
	entry string
}

func (h *pasteFailsAsAnEntryIsRead) Paste(string, string) error {
	h.seat.fold(obs{kind: "user", text: h.entry})
	return errors.New("no such pane")
}

// okDeliveries are the sends a seat said arrived.
func okDeliveries(evs []Event) []string {
	var ids []string
	for _, ev := range deliveries(evs) {
		if ev.OK {
			ids = append(ids, ev.SendID)
		}
	}
	return ids
}

// Round 14, finding 7: a send whose hand-over or paste failed is never
// confirmed by an entry written before it that the watcher reads
// meanwhile, and its failure goes
// where it went on main: a failed hand-over is typed instead, a failed
// paste is returned to the caller.
func TestR14FailedHandOverOrPasteIsNeverConfirmed(t *testing.T) {
	saved := typeSettle
	typeSettle = 0
	t.Cleanup(func() { typeSettle = saved })
	const entry, text = "fix the build and run the tests", "run the tests"
	for _, c := range []struct {
		name  string
		agent Agent
		spec  Spec
	}{
		{"codex_queue_fails", codexAgent{}, Spec{}},
		{"claude_without_its_hook", claudeAgent{}, Spec{Spool: filepath.Join(t.TempDir(), "after_turn")}},
	} {
		t.Run("after_turn/"+c.name, func(t *testing.T) {
			h := &fakeHost{alive: true}
			s := newSeat(c.spec, handOverFails{Agent: c.agent, entry: entry}, h, "r14s-"+c.name, "thread-1")
			s.state = "working"
			// The earlier entry is the first line (at 0) of the transcript
			// the seat follows, written before the send.
			s.transcript = filepath.Join(t.TempDir(), "transcript.jsonl")
			writeLines(t, s.transcript, claudeUser(time.Now(), entry))
			if _, err := s.Send("r2", text, "after_turn"); err != nil {
				t.Fatal(err)
			}
			h.mu.Lock()
			typed := slices.Clone(h.typed)
			h.mu.Unlock()
			if ok := okDeliveries(drain(s.events)); len(ok) != 0 || !slices.Equal(typed, []string{text}) {
				t.Fatalf("confirmed %v, typed %q; want nothing confirmed and the message typed", ok, typed)
			}
		})
	}
	t.Run("after_turn/handed_over_then_arrives", func(t *testing.T) {
		// A hand-over that works is confirmed by the entry that follows it.
		s := newSeat(Spec{}, handOverWorks{codexAgent{}}, &fakeHost{alive: true}, "r14s-works", "thread-1")
		s.state = "working"
		if _, err := s.Send("r2", text, "after_turn"); err != nil {
			t.Fatal(err)
		}
		s.fold(obs{kind: "user", text: text})
		if ok := okDeliveries(drain(s.events)); !slices.Equal(ok, []string{"r2"}) {
			t.Fatalf("confirmed %v; want r2", ok)
		}
	})
	t.Run("typed/paste_fails", func(t *testing.T) {
		h := &pasteFailsAsAnEntryIsRead{fakeHost: &fakeHost{alive: true}, entry: entry}
		s := newSeat(Spec{}, claudeAgent{}, h, "r14s-paste", "")
		h.seat = s
		s.state = "idle"
		// As above: the earlier entry is the transcript's first line.
		s.transcript = filepath.Join(t.TempDir(), "transcript.jsonl")
		writeLines(t, s.transcript, claudeUser(time.Now(), entry))
		msg, err := s.Send("r2", text, "queue")
		if err == nil {
			t.Fatalf("Send said %q; the paste failed", msg)
		}
		if ok := okDeliveries(drain(s.events)); len(ok) != 0 || len(s.Snapshot().Pending) != 0 {
			t.Fatalf("confirmed %v, pending %v", ok, s.Snapshot().Pending)
		}
	})
}

// Round 14, finding 9: eagent stopped while pi was in a turn on one
// session; pi then moved to another (/new, /resume) and is idle there with
// no dialog, as its status file says. The reattached seat's catch-up ends
// idle, not with the old session's working.
func TestR14PiReattachMovedToAnIdleSessionEndsIdle(t *testing.T) {
	isolatePi(t)
	since := time.Now().Add(-time.Minute)
	noFile := func(*testing.T, string) *int { return entries(0) }
	for _, c := range []struct {
		name          string
		state, prompt string                               // pi's status there
		second        func(t *testing.T, path string) *int // writes the session pi moved to; its entries at open
		want          string
	}{
		{"new_no_file_yet", "idle", "", noFile, "idle"},
		{"resume_idle_history", "idle", "", func(t *testing.T, path string) *int {
			writeLines(t, path, append([]string{piHeaderLine("second", since.Add(-2*time.Hour))}, piTurnLines("h1", "list the files", "Listed.", since.Add(-2*time.Hour))...)...)
			return entries(4)
		}, "idle"},
		// Left as they were: pi working there, or a dialog up.
		{"new_working", "working", "", noFile, "working"},
		{"new_dialog", "idle", "Allow rm -rf build?", noFile, "blocked"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			first, second, status := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "second.jsonl"), filepath.Join(dir, "pi-status.json")
			writeLines(t, first, piHeaderLine("first", since.Add(-time.Hour)), piUserLine("f1", "sort the shelves", since.Add(-time.Minute)))
			writePiStatus(t, status, piStatus{State: c.state, Prompt: c.prompt, SessionFile: second, SessionID: "second", SessionEntries: c.second(t, second)})
			s := attachSeat(Spec{StatusFile: status, Dir: dir, State: "working"}, piAgent{label: "Pi"}, &fakeHost{alive: true, screen: piFooterScreen}, "r14s-"+filepath.Base(dir), "first", first, since)
			s.poll = piLook
			go s.watch()
			t.Cleanup(s.Close)
			if ev := nextState(t, s.events); ev.State != c.want {
				t.Fatalf("catch-up said %q; want %q (pi's status there: %s %q)", ev.State, c.want, c.state, c.prompt)
			}
			if st := s.Snapshot().State; st != c.want {
				t.Fatalf("seat says %q; want %q", st, c.want)
			}
		})
	}
}
