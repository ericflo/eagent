package harness

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// Scaled from the real numbers (reading 15 min, writing 4 min, one call
// 4 min): the model looks something up, and with a little of the reading
// time left it writes its briefing on its own, which takes as long as half
// a call may (2 min of a 4 min call, here 2 s with the writing time 2 s).
//
// Before the pass had one deadline, that call ran to its end and the
// model's briefing was kept. Now a call made in the reading time may run
// only half the writing time past it, so the briefing being written is cut
// off, the model is asked to write again with what is left (1 s), and the
// harness writes the dossier: a map the model was writing is thrown away
// for a summary built from the log.
func TestR2VerifyBriefingBegunInTheReadingTimeIsNotCutOff(t *testing.T) {
	r2DossierTime(t, time.Second, 2*time.Second)
	var mu sync.Mutex
	var told []string
	s, _ := audit2Dossier(t, func(n int, msgs []map[string]any) reply {
		mu.Lock()
		told = append(told, lastText(msgs))
		mu.Unlock()
		if n == 1 {
			return reply{calls: []event.ToolCall{tc("session_search", `{"query":"PR"}`)}, block: after(700 * time.Millisecond)}
		}
		return reply{text: briefing(places(dossierRequest(msgs))[:3]), block: after(2 * time.Second)}
	})
	if d := s.dossiers()[0]; d.By != "eagent" {
		mu.Lock()
		defer mu.Unlock()
		var notes []string
		for _, s := range told {
			notes = append(notes, s[max(0, len(s)-120):])
		}
		t.Fatalf("the briefing the model was writing was cut off and the harness wrote the dossier (by %q); the calls ended with:\n%s", d.By, strings.Join(notes, "\n---\n"))
	}
}
