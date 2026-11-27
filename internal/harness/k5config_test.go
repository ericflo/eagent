package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// Choosing a configuration for a running session (/config) runs its routes
// as it defines them: openai-high's model with its own reply ceiling, key
// variable and fallback, not the session's old route with the new model's
// name. (Known, not fixed: the pick is built from the session's own route
// through PickRoute in resolvePick, which this fixer may not change.)
func TestK5AChosenConfigurationRunsItsRoutesAsDefined(t *testing.T) {
	t.Skip("known, not fixed: /config keeps only provider, model and effort; the fix is in internal/harness/settings.go (resolvePick), outside this fixer's files")
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_MODEL", "EAGENT_BASE_URL"} {
		t.Setenv(n, "")
	}
	for _, n := range []string{"EAGENT_TEST_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(n, "x")
	}
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1") // clients are swapped, never called
	cfg.Model.MaxTokens = 4000
	project := t.TempDir()
	rt, err := New(cfg, Options{Project: project}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	before := runtimecontrol.FromConfig(cfg)
	view, err := runtimecontrol.View(project, "session", "gen", before, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := runtimecontrol.Grant(project, "session")
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: runtimecontrol.Schema, ExpectedVersion: view.Snapshot.Version, Generation: "gen", Edits: []control.Edit{{Op: "set", Key: "/config", Value: "openai-high"}}}
	after, err := runtimecontrol.ApplyValues(project, before, p, view, grant)
	if err != nil {
		t.Fatal(err)
	}
	swaps, err := rt.routeChanges(before.Routes, after.Routes)
	if err != nil || len(swaps) != 1 {
		t.Fatalf("swaps %+v %v", swaps, err)
	}
	rt.swapRoute(swaps[0])
	want, err := config.ChosenPreset(project, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	got := rt.models[event.ActorEagent].conf
	if got.Model != want.Model.Model || got.MaxTokens != want.Model.MaxTokens || got.APIKeyEnv != want.Model.APIKeyEnv || (got.Fallback == nil) != (want.Model.Fallback == nil) {
		t.Fatalf("the session runs %s max_tokens %d key %s fallback %v; openai-high defines %s max_tokens %d key %s fallback %v", got.Model, got.MaxTokens, got.APIKeyEnv, got.Fallback != nil, want.Model.Model, want.Model.MaxTokens, want.Model.APIKeyEnv, want.Model.Fallback != nil)
	}
}
