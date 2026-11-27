package harness

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// A batch session whose user has heard nothing reaches its closing turn.
// The model sets a short timer there and holds; the timer fires, the model
// holds again, and the session ends. A session with no terminal does not end
// in silence: the user is owed a last word, and before 877b44a the hold was
// sent back ("the user is owed a last word") and the model's report went out.
func TestRV11ClosingTurnThatSetsATimerAndHoldsEndsInSilence(t *testing.T) {
	var phase, fired atomic.Int32
	var mu sync.Mutex
	var seen []string
	model := audit2Model(t, func(_ string, msgs []map[string]any) reply {
		tail, last := lastUserText(msgs), msgs[len(msgs)-1]["role"]
		mu.Lock()
		seen = append(seen, fmt.Sprintf("%v: %s", last, clipRunes(tail, 200)))
		mu.Unlock()
		switch {
		case last == "tool":
			return hold()
		case strings.Contains(tail, "owed a last word"):
			return say("Done: deployed to staging; a check is set for staging.")
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			return reply{calls: []event.ToolCall{tc("schedule", `{"spec":"in 300ms","note":"Check that staging is still up."}`)}}
		case strings.Contains(tail, "finished a turn") && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		case phase.Load() >= 2 && strings.Contains(tail, "staging is still up"):
			fired.Add(1)
		}
		return hold()
	})
	s := audit2Start(t, testConfig(model.srv.URL), Options{Prompt: brief}, newFakeSeat("Deployed to staging."), model, nil)
	code := coreExit(s)
	said := s.ui.said()
	t.Logf("exit %d, phase %d, fire looks %d, said %q, turn ends %q/%q", code, phase.Load(), fired.Load(), said, r11Ends(s, "done"), r11Ends(s, "error, closing"))
	mu.Lock()
	for _, x := range seen {
		t.Logf("call %s", x)
	}
	mu.Unlock()
	if phase.Load() < 2 {
		t.Fatalf("the closing turn never set the timer (phase %d)", phase.Load())
	}
	if len(said) == 0 {
		t.Fatalf("exit %d: the session ended and the user never heard a word", code)
	}
}
