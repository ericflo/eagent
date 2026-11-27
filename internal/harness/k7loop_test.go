package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The sibling of cd6c922 (C5) that it left: a session with no terminal
// stops its agent and reports, and that last ordinary turn (not the
// closing look) puts the conversation past the restart size. At the quiet
// moment right after it, maybeTurn runs before maybeEnd in the same tick:
// the session is not yet "ending" (stillClosing: no closing word asked),
// so the quiet-moment restart is made, and a dossier pass is paid for
// before a session that nothing keeps open asks its closing word and ends.
// Known limit (seventh review's notes): "a batch session that is ending can
// pay for a dossier pass it does not need".
func TestSR5LoopABatchSessionAboutToEndPaysForADossier(t *testing.T) {
	var passes, closing atomic.Int32
	_, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			passes.Add(1)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case strings.Contains(lastUserText(m), "The session is ending now"):
			closing.Add(1)
			return hold()
		case m[len(m)-1]["role"] == "tool" || strings.Contains(lastUserText(m), "Stopped Codex"):
			// The last call of the turn that stopped the agent and reported:
			// past the 20,000 restart size of testConfig, well inside the
			// window.
			r := hold()
			r.prompt = 30000
			return r
		}
		return stopsAndReports("The build is green; stopping Codex.")
	})
	var code int
	select {
	case code = <-done:
		done <- code
	case <-time.After(15 * time.Second):
		t.Fatalf("the session did not end; said %q", ui.said())
	}
	if code != 0 || closing.Load() == 0 {
		t.Fatalf("setup: exit %d, %d closing looks; said %q", code, closing.Load(), ui.said())
	}
	if n := passes.Load(); n != 0 {
		t.Fatalf("%d dossier pass(es) paid for by a session with nothing to keep it open, only to ask its closing word on the new conversation (the old one fit)", n)
	}
}
