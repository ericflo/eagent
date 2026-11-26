package relay

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// agyLines is one Antigravity step pair, stamped to the whole second as
// agy writes them: the user's request and the reply that ends the turn.
func agyLines(user, reply string, at time.Time) []string {
	ts := at.UTC().Truncate(time.Second).Format(time.RFC3339)
	return []string{
		fmt.Sprintf(`{"type":"USER_INPUT","source":"USER_EXPLICIT","content":%q,"created_at":%q}`, "<USER_REQUEST>\n"+user+"\n</USER_REQUEST>", ts),
		fmt.Sprintf(`{"type":"PLANNER_RESPONSE","content":%q,"created_at":%q}`, reply, ts),
	}
}

// r11Case is one agent's transcript of a turn at at, stamped as it stamps.
type r11Case struct {
	name  string
	agent Agent
	unit  time.Duration
	turn  func(user, reply string, at time.Time) []string
}

func r11Cases() []r11Case {
	return []r11Case{
		{"pi", piAgent{label: "Pi"}, time.Millisecond, func(user, reply string, at time.Time) []string {
			return append([]string{piHeaderLine("p1", at.Add(-time.Hour)), piModelLine(at.Add(-time.Hour))}, piTurnLines("t-"+reply, user, reply, at)...)
		}},
		{"antigravity", antigravityAgent{}, time.Second, agyLines},
		{"claude", claudeAgent{}, time.Millisecond, claudeTurnLines},
	}
}

// settled waits for the state the catch-up says at its end, which comes
// after everything the history and the news emitted.
func (w *reattachedSeat) settled(t *testing.T) {
	t.Helper()
	w.caughtUp(t)
	if !eventually(func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return slices.ContainsFunc(w.events, func(ev Event) bool { return ev.Kind == KindState })
	}) {
		t.Fatal("the catch-up never said a state")
	}
}

// The finding's cases with the watermark the harness now gives: the reply
// to a send written in the send's own millisecond or second, while eagent
// was down, is read after the last event the transcript gave (the turn
// before), so its report comes once and the send is confirmed once; the
// turn before is not reported again.
func TestAReplyInTheSendsOwnStampIsReportedOnceAfterAReattach(t *testing.T) {
	t.Parallel()
	for _, c := range r11Cases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			prev := time.Now().Add(-time.Hour).Truncate(time.Second)
			since := prev.Add(650 * time.Millisecond) // the turn before, recorded when read
			sent := prev.Add(5*time.Second + 123456*time.Microsecond)
			dir := t.TempDir()
			path := filepath.Join(dir, "t.jsonl")
			writeLines(t, path, append(c.turn("hello", "Ready.", prev), c.turn("what is 2+2", "4", sent.Truncate(c.unit))[c.skip():]...)...)
			w := reattach(t, c.agent, Spec{Dir: dir}, path, since, Restored{ID: "r1", Text: "what is 2+2", Mode: "queue", At: sent})
			w.settled(t)
			if !eventually(func() bool { return len(w.turns()) > 0 }) {
				t.Fatalf("the reply was dropped: turns %q", w.turns())
			}
			if turns := w.turns(); !slices.Equal(turns, []string{"4"}) {
				t.Fatalf("turns %q, want the reply once", turns)
			}
			if ok, failed := w.delivered(); !slices.Equal(ok, []string{"r1"}) || len(failed) > 0 {
				t.Fatalf("confirmed %v, not confirmed %v; want r1 once", ok, failed)
			}
		})
	}
}

// skip is how many lines of a second turn repeat the first's header.
func (c r11Case) skip() int {
	if c.name == "pi" {
		return 2
	}
	return 0
}

// What the seat read before eagent stopped is not news again. The turn's
// report was recorded when the seat read it, in the same second as agy
// stamped it (or, for pi and Claude, the same millisecond): the watermark
// is that record's time, and the turn, stamped earlier within its unit, is
// history. Comparing at the stamp's precision would report it again.
func TestATurnReadInTheWatermarksOwnStampIsNotReportedAgain(t *testing.T) {
	t.Parallel()
	for _, c := range r11Cases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			at := time.Now().Add(-time.Hour).Truncate(time.Second)
			lines := c.turn("hello", "Ready.", at)
			since := at.Add(650 * time.Millisecond) // agy: read and recorded within the second
			if c.unit == time.Millisecond {
				// The last line is the turn's end, 3 ms (pi) or 2 ms (Claude)
				// after at: recorded within that millisecond.
				end := map[string]time.Duration{"pi": 3 * time.Millisecond, "claude": 2 * time.Millisecond}[c.name]
				since = at.Add(end + 400*time.Microsecond)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "t.jsonl")
			writeLines(t, path, lines...)
			w := reattach(t, c.agent, Spec{Dir: dir}, path, since)
			w.settled(t)
			if turns := w.turns(); len(turns) > 0 {
				t.Fatalf("a turn read before eagent stopped was reported again: %q", turns)
			}
		})
	}
}
