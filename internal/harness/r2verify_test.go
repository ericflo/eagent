package harness

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
)

// Scaled from the real numbers (reading 15 min, one call 4 min, writing
// two calls long): the model looks something up, and with a little of the
// reading time left it writes its briefing on its own, which takes most of
// a call. That call runs to its end and the model's briefing is kept. With
// a writing time only one call long, a call begun in the reading time was
// cut off half a call past it: the briefing being written was thrown away,
// the model was asked again with half a call left, and the harness wrote
// the dossier.
func TestR2VerifyBriefingBegunInTheReadingTimeIsNotCutOff(t *testing.T) {
	if dossierWriteTime < 2*dossierTimeout {
		t.Fatalf("the writing time (%s) is under two calls (%s each): a call begun in the reading time would be cut short", dossierWriteTime, dossierTimeout)
	}
	r2DossierTime(t, time.Second, 6*time.Second) // a call is 3 s here
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

// A draft that was sent back for another try is not thrown away when the
// pass runs out of time before the second one comes: it is kept as it
// stands and labelled, as a draft that came back wrong twice is.
func TestR2VerifyDraftInHandIsKeptWhenThePassRunsOutOfTime(t *testing.T) {
	r2DossierTime(t, 300*time.Millisecond, 1500*time.Millisecond)
	s, _ := audit2Dossier(t, func(n int, msgs []map[string]any) reply {
		if n == 1 {
			// One place under three topics: not a map, so it is sent back.
			p := places(dossierRequest(msgs))[0]
			return reply{text: briefing([]string{p, p, p})}
		}
		return reply{text: "never arrives", block: after(20 * time.Second)}
	})
	d := s.dossiers()[0]
	if d.By != "eagent" || !d.NoMap || !strings.Contains(d.Text, "WHAT THE USER WANTS") {
		t.Fatalf("the draft in hand was not kept (by %q, labelled %v):\n%s", d.By, d.NoMap, d.Text)
	}
}
