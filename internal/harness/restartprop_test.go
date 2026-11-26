package harness

import (
	"fmt"
	"math/rand"
	"testing"
)

// The seeded randomised check of the restart decision (restartHelps,
// causeOf). Sizes are drawn as numbers and turned into restartFacts the way
// restartFactsNow does: every size in the provider's count (estimate times
// the drawn ratio), the opening against the window's input room when the
// provider refused the conversation, and the opening plus room for one
// message against the restart size (half the window) when it did not.

const restartPropSeed = 20261126

const restartPropDraws = 20000

// restartDraw is one drawn situation, kept whole so a failure prints it.
type restartDraw struct {
	window     int     // the model's window, 4,000 to 1,000,000
	fixed      int     // eagent's estimate of system prompt, tools and briefing
	carry      int     // eagent's estimate of the room for one message
	ratio      float64 // provider count / eagent's estimate, 1.0 to 2.5
	overflowed bool    // the provider refused the conversation as too long
	calls      int     // calls of this conversation that went through
	rescues    int     // restarts in a row made for size, 0 to 5
	told       bool    // the user was already told it cannot fit
	carried    carried // what the restart that began it carried whole
	since      bool    // events after the briefing were in the refused request
	retried    bool    // the restart that began it was the one more try
}

func (d restartDraw) String() string {
	return fmt.Sprintf("window=%d fixed=%d carry=%d ratio=%.4f overflowed=%v calls=%d rescues=%d told=%v carried=%d sinceOpening=%v retried=%v",
		d.window, d.fixed, d.carry, d.ratio, d.overflowed, d.calls, d.rescues, d.told, d.carried, d.since, d.retried)
}

func drawRestart(rng *rand.Rand) restartDraw {
	var d restartDraw
	switch rng.Intn(4) {
	case 0:
		d.window = 4000
	case 1:
		d.window = 1000000
	default:
		d.window = 4000 + rng.Intn(1000000-4000+1)
	}
	// Fixed part from tiny to larger than the window.
	switch rng.Intn(4) {
	case 0:
		d.fixed = 1 + rng.Intn(200)
	case 1:
		d.fixed = d.window + rng.Intn(d.window+1)
	default:
		d.fixed = 1 + rng.Intn(d.window*3/2)
	}
	d.carry = rng.Intn(d.window/2 + 1)
	switch rng.Intn(4) {
	case 0:
		d.ratio = 1.0
	case 1:
		d.ratio = 2.5
	default:
		d.ratio = 1.0 + 1.5*rng.Float64()
	}
	d.overflowed = rng.Intn(2) == 0
	if rng.Intn(2) == 0 {
		d.calls = 1 + rng.Intn(50)
	}
	d.rescues = rng.Intn(6)
	d.told = rng.Intn(2) == 0
	d.carried = carried(rng.Intn(3))
	d.since = rng.Intn(2) == 0
	d.retried = rng.Intn(2) == 0
	return d
}

// facts turns a draw into restartFacts as restartFactsNow does.
func (d restartDraw) facts() restartFacts {
	provider := func(n int) int { return int(float64(n) * d.ratio) }
	input := d.window * 9 / 10 // the window less the reply's room
	limit := d.window / 2      // the restart size: half the window
	f := restartFacts{
		refused:           d.overflowed && d.calls == 0,
		refusedAfterCalls: d.overflowed && d.calls > 0,
		carried:           d.carried,
		retried:           d.retried,
		rescues:           d.rescues,
		decided:           d.told,
	}
	opening := provider(d.fixed)
	if f.refused {
		f.sinceOpening = d.since
		f.openingAloneFits = opening < input
		return f
	}
	f.openingFits = opening+provider(d.carry) < limit
	return f
}

// restartPropFail reports a failing draw with the seed and draw index.
func restartPropFail(t *testing.T, i int, d restartDraw, format string, args ...any) {
	t.Helper()
	t.Fatalf("seed=%d draw=%d %v: %s", restartPropSeed, i, d, fmt.Sprintf(format, args...))
}

// TestRestartPropOutcomes: restartHelps decides between exactly two
// outcomes, restart (true) and tell the user it cannot fit (false); it is a
// bool, so never both. "Fits" is not one of its outcomes: it is only asked
// once the conversation did not fit (refused, or past the restart size on
// its first call). Whenever it says no, causeOf names one of the three
// notice wordings, so the user is told something true (needsDossier records
// the notice unless toldFull, and tellStillFull after), never silence. A
// refusal the opening alone explains is never worded as a burst.
func TestRestartPropOutcomes(t *testing.T) {
	rng := rand.New(rand.NewSource(restartPropSeed))
	var seen [2][3]int // [helps][fullCause] when told, [1][0] when restarted
	defer func() {
		if !t.Failed() && (seen[1][0] == 0 || seen[0][fullOpening] == 0 || seen[0][fullBurst] == 0 || seen[0][fullAgain] == 0) {
			t.Fatalf("seed=%d: the draws miss an outcome: %v", restartPropSeed, seen)
		}
	}()
	for i := range restartPropDraws {
		d := drawRestart(rng)
		f := d.facts()
		if restartHelps(f) {
			seen[1][0]++
			continue
		}
		if c := causeOf(f); c >= 0 && int(c) < 3 {
			seen[0][c]++
		}
		if f.refusedAfterCalls {
			restartPropFail(t, i, d, "a refusal after calls went through was not restarted")
		}
		switch c := causeOf(f); c {
		case fullOpening, fullBurst, fullAgain:
			if f.refused && !f.openingAloneFits && c != fullOpening {
				restartPropFail(t, i, d, "the opening alone does not fit, but the notice says %d", c)
			}
		default:
			restartPropFail(t, i, d, "no notice wording for cause %d", c)
		}
	}
}

// TestRestartPropNoFlipFlop: the same facts with one more restart already
// made in a row never turn "tell the user" back into "restart". The
// documented exception, a refusal after calls went through, always
// restarts whatever the count, so it is the same on both sides here.
func TestRestartPropNoFlipFlop(t *testing.T) {
	rng := rand.New(rand.NewSource(restartPropSeed + 1))
	for i := range restartPropDraws {
		d := drawRestart(rng)
		before := restartHelps(d.facts())
		more := d
		more.rescues++
		after := restartHelps(more.facts())
		if !before && after && !d.facts().refusedAfterCalls {
			restartPropFail(t, i, d, "told at rescues=%d, restart at rescues=%d", d.rescues, more.rescues)
		}
		if d.facts().refusedAfterCalls && !(before && after) {
			restartPropFail(t, i, d, "a refusal after calls went through was not always restarted")
		}
	}
}

// TestRestartPropBounded: from any drawn facts, feeding each restart back
// in as startDossier and the restart's landing do (a rescue counts one more
// in a row unless the conversation grew to the window with calls through,
// which starts the count again at 0; the new conversation carried nothing,
// was retried when the refused one carried nothing, is not decided, and no
// call goes through) reaches "tell the user" within 4 steps, while the
// conversation stays too long from its start every time (the worst case:
// the same sizes again, something arriving during every pass).
func TestRestartPropBounded(t *testing.T) {
	rng := rand.New(rand.NewSource(restartPropSeed + 2))
	for i := range restartPropDraws {
		d := drawRestart(rng)
		cur := d
		steps := 0
		for restartHelps(cur.facts()) {
			steps++
			if steps > 4 {
				restartPropFail(t, i, d, "still restarting after 4 restarts in a row (now %v)", cur)
			}
			f := cur.facts()
			next := cur
			next.retried = f.refused && cur.carried == carriedNothing
			if f.refusedAfterCalls {
				next.rescues = 0
			} else {
				next.rescues = cur.rescues + 1
			}
			next.carried = carriedNothing
			next.told = false
			next.calls = 0
			next.since = true
			cur = next
		}
	}
}
