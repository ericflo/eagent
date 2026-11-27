package harness

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// The same model at the same address written with a trailing slash is
// the route eagent is on, both to the loop (sameRoute: the model keeps
// its failures, restarts in a row and token ratio) and to the record (no
// move: the dossier's restarts in a row and the size of the first call
// stand), so a resumed run reads it as the running one did.
func TestK2AnAddressWithATrailingSlashIsNoMove(t *testing.T) {
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	s.settled()
	var before, after int64
	s.rt.sync(func() { before = s.rt.st.Moved() })
	conf := s.rt.cfg.Model
	old := conf.BaseURL
	conf.BaseURL += "/"
	eps, err := conf.Routes()
	if err != nil {
		t.Fatal(err)
	}
	moved, on := true, false
	s.rt.sync(func() {
		moved = !sameRoute(s.rt.models[event.ActorEagent].client.Endpoint, eps[0])
		s.rt.swapRoute(routeSwap{who: event.ActorEagent, pick: runtimecontrol.Route{Provider: "test", Model: conf.Model}, eps: eps, conf: conf})
		after = s.rt.st.Moved()
		// A reply still out from before, from the address as it was
		// written then, came from the route in use.
		on = s.rt.st.OnRoute(event.ActorEagent, old, conf.Model)
	})
	if moved {
		t.Fatalf("setup: the loop takes %q for another address", eps[0].BaseURL)
	}
	if after != before {
		t.Fatalf("the record took %q for a move (seq %d), the loop for the same route", eps[0].BaseURL, after)
	}
	if !on {
		t.Fatalf("a reply from %q is not taken for the route in use, now written %q", old, eps[0].BaseURL)
	}
}

// C5: a session with no terminal stops its agent and reports; its closing
// look puts the conversation past the restart size. The session is ending,
// and nothing will be asked of that conversation again: no dossier pass is
// paid for before it ends.
func TestK2AnEndingBatchSessionPaysForNoDossier(t *testing.T) {
	var passes atomic.Int32
	_, ui, done := k2Batch(t, "@e check the build", func(_ string, m []map[string]any) reply {
		if ask := dossierRequest(m); ask != "" {
			passes.Add(1)
			p := places(ask)
			return reply{text: briefing(p[max(0, len(p)-3):])}
		}
		switch {
		case m[len(m)-1]["role"] == "tool":
			return hold()
		case strings.Contains(lastUserText(m), "The session is ending now"):
			r := hold()
			r.prompt = 30000 // past the 20,000 restart size of testConfig
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
	if code != 0 {
		t.Fatalf("exit %d; said %q", code, ui.said())
	}
	if n := passes.Load(); n != 0 {
		t.Fatalf("%d dossier passes paid for by a session that was ending", n)
	}
}
