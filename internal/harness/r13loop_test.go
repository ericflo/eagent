package harness

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ericflo/eagent/internal/event"
)

// Findings 1-6 of the thirteenth review: what stillClosing is asked, and
// where it was not.

// ---- 3. what keeps a session open -----------------------------------------

// The user upgrades the stopped agent (/upgrade) while the first call of the
// closing turn is out, and that call fails. The upgrade's work keeps the
// session open, as maybeEnd has it: eagent does not say the session is
// ending with nothing running.
func TestR13UpgradeDuringAFailingClosingCallGetsNoClosingWords(t *testing.T) {
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	old := upgradeAgent
	upgradeAgent = func(string) (string, error) { <-gate; return "upgraded", nil }
	t.Cleanup(func() { release(); upgradeAgent = old })
	var phase atomic.Int32
	s, _ := r12Start(t, newFakeSeat("Merged #42; the tests are green."), func(rt *atomic.Pointer[Runtime], msgs []map[string]any) reply {
		tail := lastUserText(msgs)
		switch {
		case strings.Contains(tail, "The session is ending now") && phase.CompareAndSwap(1, 2):
			r := rt.Load()
			r.sync(func() { r.onInputFrom("/upgrade", "web", "", "", nil) })
			return r10Bad
		case strings.Contains(tail, "finished a turn") && msgs[len(msgs)-1]["role"] != "tool" && phase.CompareAndSwap(0, 1):
			return stopsWhenDone()
		}
		return hold()
	})
	s.waitFor("the failed closing call", func() bool { return len(s.events(event.Error)) > 0 })
	release()
	s.waitFor("the upgrade's outcome", func() bool { return saidWith(s.ui, "Upgraded") })
	s.settled()
	if r5Told(s, "This session is ending") != 0 {
		t.Fatalf("said %q, with the agent upgraded and running again", s.ui.said())
	}
}
