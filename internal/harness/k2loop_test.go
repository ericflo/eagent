package harness

import (
	"testing"

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
