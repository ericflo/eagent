package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// A live pick names the effort the session reports (runtimecontrol
// ApplyValues resolves it), and the session sends exactly that, "" (send
// none) included, whatever effort its configured route had.
func TestALivePickSendsTheEffortItNames(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("OPENROUTER_API_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	for _, c := range []struct{ configured, picked string }{
		{"xhigh", ""},   // a configuration chosen live whose effort is ""
		{"", ""},        // a model switched with no effort named
		{"", "medium"},  // an effort named
		{"high", "low"}, // an effort named over another
	} {
		cfg := testConfig("http://127.0.0.1:1") // clients are swapped, never called
		cfg.Model.ReasoningEffort = c.configured
		rt, err := New(cfg, Options{Project: t.TempDir()}, &fakeUI{input: make(chan string)})
		if err != nil {
			t.Fatal(err)
		}
		before := runtimecontrol.FromConfig(cfg).Routes
		after := map[string]runtimecontrol.Route{runtimecontrol.RouteEagent: {Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: c.picked}}
		swaps, err := rt.routeChanges(before, after)
		rt.sess.Close()
		if err != nil || len(swaps) != 1 {
			t.Fatalf("%+v: %+v %v", c, swaps, err)
		}
		if got := swaps[0].eps[0].ReasoningEffort; got != c.picked {
			t.Errorf("configured %q, picked %q: sends %q", c.configured, c.picked, got)
		}
	}
}
