//go:build gaps

package harness

import (
	"strings"
	"sync"
	"testing"
)

// GAP (old TestNarratorMessageNamingTheMachineryIsSentBackOnce): on main the
// words the user never hears were orchestrator, narrator, task worker,
// dossier, subsession, rollover, context reset, harness, tool call and long
// think (narrator.go, mechanicsWords). The one loop's list (loop.go) lost
// orchestrator, narrator, task worker and long think, so a message that
// names them reaches the user unrewritten. The old test's message was "The
// orchestrator finished the build after a rollover." and the rewrite
// request named "(orchestrator, rollover)".
func TestCoreGapMechanicsWordsOfTheOldDesign(t *testing.T) {
	if got := mechanicsLeak("The orchestrator finished the build after a rollover."); got != "orchestrator, rollover" {
		t.Errorf("mechanicsLeak = %q, want %q", got, "orchestrator, rollover")
	}
	if got := mechanicsLeak("The narrator says a task worker is in a long think."); got != "narrator, task worker, long think" {
		t.Errorf("mechanicsLeak = %q, want %q", got, "narrator, task worker, long think")
	}

	var mu sync.Mutex
	attempts := 0
	s := startSession(t, brief, func(_ string, msgs []map[string]any) reply {
		if !strings.Contains(allText(msgs), "finished a turn") {
			return hold()
		}
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts == 1 {
			return say("The orchestrator finished the build.")
		}
		if !strings.Contains(allText(msgs), "names internal machinery (orchestrator)") {
			return say("the rewrite request did not arrive")
		}
		return say("I finished the build.")
	}, nil)
	s.waitFor("the report", func() bool { return s.ui.messageCount() == 1 })
	s.settled()
	if got := s.ui.said(); len(got) != 1 || got[0] != "I finished the build." {
		t.Fatalf("said %q", got)
	}
}
