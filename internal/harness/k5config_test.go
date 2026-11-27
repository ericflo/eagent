package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// A preset saved in the same proposal that chooses a configuration holds
// that configuration's routes as it defines them, as the session will run
// them: the controller's save path (controller.go) asks runningRoutes for
// the routes, through ChosenRoutes with the configuration's name when the
// change chooses one, and builds the preset from them.
func TestK5APresetSavedWithAChosenConfigurationHoldsItsRoutes(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_MODEL", "EAGENT_BASE_URL"} {
		t.Setenv(n, "")
	}
	for _, n := range []string{"EAGENT_TEST_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(n, "x")
	}
	useFakeRelays(t, newFakeSeat("ok"))
	cfg := testConfig("http://127.0.0.1:1")
	cfg.Model.MaxTokens = 4000
	project := t.TempDir()
	rt, err := New(cfg, Options{Project: project}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	before := rt.activeSettings
	view, err := runtimecontrol.View(project, "session", "gen", before, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := runtimecontrol.Grant(project, "session")
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: runtimecontrol.Schema, ExpectedVersion: view.Snapshot.Version, Generation: "gen", Edits: []control.Edit{{Op: "set", Key: "/config", Value: "openai-high"}, {Op: "set", Key: "/save_preset", Value: "saved"}}}
	after, err := runtimecontrol.ApplyValues(project, before, p, view, grant)
	if err != nil {
		t.Fatal(err)
	}
	running, err := rt.runningRoutes(after.Routes, "openai-high") // as ChosenRoutes hands them over: the /config edit's name
	if after.Config == "openai-high" {
		t.Fatal("setup: the values name the chosen configuration, not the preset saved")
	}
	if err != nil {
		t.Fatal(err)
	}
	saved, err := runtimecontrol.ConfigFromRoutes(project, after, running)
	if err != nil {
		t.Fatal(err)
	}
	want, err := config.ChosenPreset(project, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Model.Model != want.Model.Model || saved.Model.MaxTokens != want.Model.MaxTokens || (saved.Model.Fallback == nil) != (want.Model.Fallback == nil) {
		t.Fatalf("the preset saved holds %s max_tokens %d fallback %v; openai-high defines %s max_tokens %d fallback %v", saved.Model.Model, saved.Model.MaxTokens, saved.Model.Fallback != nil, want.Model.Model, want.Model.MaxTokens, want.Model.Fallback != nil)
	}
}

// Choosing a configuration for a running session (/config) runs its routes
// as it defines them: openai-high's model with its own reply ceiling, key
// variable and fallback, not the session's old route with the new model's
// name. (applyRuntimeSettings passes the chosen name when the change has a
// /config edit, as here.)
func TestK5AChosenConfigurationRunsItsRoutesAsDefined(t *testing.T) {
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
	swaps, err := rt.routeChanges(before.Routes, after.Routes, "openai-high") // the /config edit's name
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
