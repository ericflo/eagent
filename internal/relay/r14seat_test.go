package relay

import (
	"path/filepath"
	"testing"
	"time"
)

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
