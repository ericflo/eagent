package relay

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// rv15: a transcript the agent replaces at the same path (the tailer starts
// it again from the top: "Truncated or replaced") holds the send's arrival
// at an offset below the size the old file had when the send was tracked.
// Also when the new content is longer than that size but shorter than what
// the watcher had read (the tailer's start again, not the size, decides).
// The watcher's read (readTail) folds what the tailer returns.
func TestRV15ReplacedTranscriptArrivalIsConfirmed(t *testing.T) {
	older := func(n int) []string {
		var lines []string
		for i := 0; i < n; i++ {
			lines = append(lines, claudeUser(time.Now(), "an older prompt with some words in it, number "+strings.Repeat("x", 40)))
		}
		return lines
	}
	for _, c := range []struct {
		name       string
		afterTrack int // older lines the agent writes after the send was tracked, read before the rewrite
		rewritten  int // older lines the rewritten transcript starts with
	}{
		{"shorter_than_the_mark", 0, 0},
		{"longer_than_the_mark", 10, 22},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			writeLines(t, path, older(20)...)
			s := newSeat(Spec{}, claudeAgent{}, &fakeHost{alive: true}, "x", "")
			s.transcript, s.state = path, "working"
			tl := &tailer{path: path}
			p := &claudeParser{}
			feed := func() {
				if err := s.readTail(tl, p, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			feed()
			s.track("r1", "run the tests", "queue")
			mark, _ := os.Stat(path)
			if c.afterTrack > 0 {
				appendLines(t, path, older(c.afterTrack)...)
				feed()
			}
			// The agent rewrites its transcript, recording the send first.
			writeLines(t, path, append([]string{claudeUser(time.Now(), "run the tests")}, older(c.rewritten)...)...)
			if st, _ := os.Stat(path); c.rewritten > 0 && (st.Size() <= mark.Size() || st.Size() >= tl.offset) {
				t.Fatalf("rewritten to %d bytes; want between the mark %d and the read %d", st.Size(), mark.Size(), tl.offset)
			}
			feed()
			if ok := okDeliveries(drain(s.events)); !slices.Equal(ok, []string{"r1"}) {
				t.Fatalf("confirmed %v; want r1 (its arrival is in the replaced transcript)", ok)
			}
		})
	}
}
