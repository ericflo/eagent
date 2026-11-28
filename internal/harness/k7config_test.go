package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// A running session chooses /config bundle:mine, a bundle whose route
// reaches OpenRouter with its own key variable (MY_OR_KEY, set here; the
// provider's OPENROUTER_API_KEY is not). The session would run it
// (routeChanges resolves the bundle's entry, 3ebd916), but the controller
// checks the key before that with Routes, not ChosenRoutes (c4bddf9 gave
// only the preset save the chosen name): the pick is resolved from the
// catalog, its OPENROUTER_API_KEY is unset, and the change is refused with
// "OPENROUTER_API_KEY is not set ... cannot use openrouter".
func TestSR2ConfigChosenBundleKeyIsTheBundles(t *testing.T) {
	relay.OfflineCatalogs()
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_MODEL", "EAGENT_BASE_URL", "OPENROUTER_API_KEY"} {
		t.Setenv(n, "")
	}
	t.Setenv("MY_OR_KEY", "x")
	s := startSession(t, "", func(string, []map[string]any) reply { return hold() }, nil)
	project, id := s.rt.opts.Project, s.rt.sess.ID
	coreControlUp(t, project, id)
	if err := os.MkdirAll(filepath.Dir(config.BundlePath(project, "mine")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(`{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3-flash","api_key_env":"MY_OR_KEY"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadBundle(project, "", "mine"); err != nil {
		t.Fatalf("setup: the bundle does not load: %v", err)
	}
	q := coreRequest(t, project, id, "choose-mine", "settings.apply", control.Edit{Op: "set", Key: "/config", Value: "bundle:mine"})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := runtimecontrol.Deliver(ctx, project, id, q, false)
	if err != nil {
		t.Fatal(err)
	}
	// What the session would run once past that check: the bundle's route
	// with its own key, which is set.
	status, _ := runtimecontrol.ReadStatus(project, id)
	view, _ := runtimecontrol.View(project, id, status.Generation, status.Values, true)
	grant, _ := runtimecontrol.Grant(project, id)
	// (Once the change is applied the proposal's version is stale, so this
	// look at what the session would run is only logged.)
	after, err := runtimecontrol.ApplyValues(project, status.Values, q.Proposal, view, grant)
	if err != nil {
		t.Logf("the proposal again over the settings after it: %v", err)
	}
	var swapErr error
	var swapKey string
	s.rt.sync(func() {
		swaps, err := s.rt.routeChanges(s.rt.activeSettings.Routes, after.Routes, "bundle:mine")
		swapErr = err
		if err == nil && len(swaps) == 1 {
			swapKey = swaps[0].conf.APIKeyEnv
		}
	})
	t.Logf("routeChanges with the chosen name: key %q err %v", swapKey, swapErr)
	var key string
	s.rt.sync(func() { key = s.rt.models[event.ActorEagent].conf.APIKeyEnv })
	if result.Status != "succeeded" || key != "MY_OR_KEY" {
		t.Fatalf("choosing bundle:mine: status %s (%v); the session's key variable is %q, want the bundle's MY_OR_KEY", result.Status, result.Result["message"], key)
	}
}

// A session that already runs openai-high's model at its effort, from a
// route of its own (reply ceiling 4000, no fallback), chooses /config
// openai-high. The settings now name openai-high, and 3ebd916 says a
// configuration chosen for a running session runs its routes as it defines
// them; but routeChanges skips a pick equal to the one before, so the
// session keeps its own ceiling and no fallback, and a preset saved in the
// same change (runningRoutes, c4bddf9) holds them too.
func TestSR2ConfigChosenConfigurationWithTheSameModelRunsAsDefined(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_MODEL", "EAGENT_BASE_URL"} {
		t.Setenv(n, "")
	}
	for _, n := range []string{"EAGENT_TEST_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(n, "x")
	}
	useFakeRelays(t, newFakeSeat("ok"))
	project := t.TempDir()
	want, err := config.ChosenPreset(project, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	if want.Model.Fallback == nil {
		t.Fatal("setup: openai-high has no fallback")
	}
	cfg := testConfig("http://127.0.0.1:1")
	cfg.Model = want.Model
	cfg.Model.MaxTokens = 4000
	cfg.Model.Fallback = nil
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
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: runtimecontrol.Schema, ExpectedVersion: view.Snapshot.Version, Generation: "gen", Edits: []control.Edit{{Op: "set", Key: "/config", Value: "openai-high"}}}
	after, err := runtimecontrol.ApplyValues(project, before, p, view, grant)
	if err != nil {
		t.Fatal(err)
	}
	if after.Config != "openai-high" {
		t.Fatalf("setup: the settings name %q", after.Config)
	}
	t.Logf("pick before %+v after %+v", before.Routes[runtimecontrol.RouteEagent], after.Routes[runtimecontrol.RouteEagent])
	swaps, err := rt.routeChanges(before.Routes, after.Routes, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	if len(swaps) != 1 || swaps[0].conf.MaxTokens != want.Model.MaxTokens || swaps[0].conf.Fallback == nil {
		t.Errorf("choosing openai-high made %d route swaps; the session keeps max_tokens %d and no fallback while its settings name openai-high (max_tokens %d, with a fallback)", len(swaps), rt.models["eagent"].conf.MaxTokens, want.Model.MaxTokens)
	}
	running, err := rt.runningRoutes(after.Routes, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	if got := running[runtimecontrol.RouteEagent]; got.MaxTokens != want.Model.MaxTokens || got.Fallback == nil {
		t.Errorf("a preset saved with /config openai-high holds max_tokens %d fallback %v; openai-high defines %d with a fallback", got.MaxTokens, got.Fallback != nil, want.Model.MaxTokens)
	}
}
