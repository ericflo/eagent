package relay

import (
	"testing"
	"time"
)

// index is where the first line with key == text is in the run, -1 when
// there is none.
func (r piPathRun) index(key, text string) int {
	for i, l := range r.lines {
		if v, ok := l[key].(string); ok && v == text {
			return i
		}
	}
	return -1
}

// A message held for the end of pi's turn, when that turn is interrupted
// and a message typed right after it (Seat.Send in interrupt mode: Escape,
// then the message 1.5 s later): the typed message runs first, and the held
// one after it, once. Handed over at the aborted settle, the held message
// started a run of its own and the urgent one waited behind it.
func TestPiHeldMessageWaitsForTheMessageAnInterruptBrings(t *testing.T) {
	t.Parallel()
	const urgent = "the urgent message"
	list, wait, _ := runPiPromptPath(t, piFast, 10*time.Second,
		`[[100, "type", "a long run"], [2000, "abort", ""], [3500, "type", "`+urgent+`"]]`, "RUN_EACH_MS=3000")
	r := wait()
	if r.index("ran", urgent) < 0 || r.count("ran", held) != 1 || r.index("ran", held) < r.index("ran", urgent) || len(list()) != 0 {
		t.Fatalf("the held message ran %d times, spool %v:\n%s", r.count("ran", held), list(), r.out)
	}
}

// An interrupt with nothing after it does not start the held message at
// once: pi stays stopped for a while (the person pressed Escape, or pi is
// quitting or changing sessions), and the message goes later, once.
func TestPiHeldMessageDoesNotStartARunAtAnInterrupt(t *testing.T) {
	t.Parallel()
	list, wait, _ := runPiPromptPath(t, piFast, 10*time.Second,
		`[[100, "type", "a long run"], [2000, "abort", ""], [5000, "mark", "three seconds after the interrupt"]]`, "RUN_EACH_MS=3000")
	r := wait()
	if r.index("ran", held) < r.index("mark", "three seconds after the interrupt") || r.count("ran", held) != 1 || len(list()) != 0 {
		t.Fatalf("the held message ran %d times, spool %v:\n%s", r.count("ran", held), list(), r.out)
	}
}
