package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// chainText writes a configured chain as the fields a saved preset must
// keep, one entry per line, so two chains compare in one string.
func chainText(a config.ModelRoute) string {
	var b strings.Builder
	for cur := &a; cur != nil; cur = cur.Fallback {
		fmt.Fprintf(&b, "%s %s %s key=%s window=%d reply=%d effort=%s\n", cur.Protocol, strings.TrimRight(cur.BaseURL, "/"), cur.Model, cur.APIKeyEnv, cur.ContextTokens, cur.MaxTokens, cur.ReasoningEffort)
	}
	return b.String()
}

// saveStart is a configuration a session can be started on, in a project
// whose config.json names the session's model too, configured otherwise,
// so a save that looks the model up by name in the wrong place shows.
type saveStart struct {
	name string
	// files: config.json and the saved presets the project has.
	configJSON string
	bundles    map[string]string
	load       func(project string) (config.Config, error)
	// own: the saved preset the session runs, "" when none.
	own string
	// pick: a model picked from the sheet, one the start does not have.
	pick runtimecontrol.Route
}

const (
	savePrimary = `{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"EAGENT_TEST_KEY","context_tokens":100000,"max_tokens":8000,"reasoning_effort":"high",
		"fallback":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1/","model":"z-ai/glm-5.3-flash","api_key_env":"EAGENT_TEST_KEY2","context_tokens":50000,"max_tokens":4000,"reasoning_effort":"low"}}`
	saveCatalog = `{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"moonshotai/kimi-k3","api_key_env":"EAGENT_TEST_KEY2","context_tokens":64000,"max_tokens":4000,"reasoning_effort":"high",
		"fallback":{"protocol":"openai-chat","base_url":"http://127.0.0.1:9/v1","model":"qwen-local","api_key_env":"EAGENT_TEST_KEY","context_tokens":32000,"max_tokens":2048}}`
	saveLocal = `{"protocol":"openai-chat","base_url":"http://127.0.0.1:9/v1","model":"qwen-local","api_key_env":"EAGENT_TEST_KEY","context_tokens":32000,"max_tokens":2048,"reasoning_effort":"low",
		"fallback":{"protocol":"openai-chat","base_url":"http://127.0.0.1:9/v1/","model":"qwen-small","api_key_env":"EAGENT_TEST_KEY","context_tokens":16000,"max_tokens":1024}}`
	// The same models as above, configured otherwise, with no fallback.
	saveDecoyKimi  = `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"moonshotai/kimi-k3","api_key_env":"EAGENT_TEST_KEY","context_tokens":200000,"max_tokens":16000}}`
	saveDecoyLocal = `{"model":{"protocol":"openai-chat","base_url":"http://127.0.0.1:9/v1","model":"qwen-local","api_key_env":"EAGENT_TEST_KEY2","context_tokens":8000,"max_tokens":512}}`
	saveOther      = `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"EAGENT_TEST_KEY2","context_tokens":64000,"max_tokens":4000}}`
)

func saveStarts(t *testing.T) []saveStart {
	ref, err := config.ChosenPreset(t.TempDir(), "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	decoy := ref.Model
	decoy.APIKeyEnv, decoy.ContextTokens, decoy.MaxTokens, decoy.Fallback = "EAGENT_TEST_KEY", 200_000, 512, nil
	decoyJSON, _ := json.Marshal(map[string]any{"model": decoy})
	glm := runtimecontrol.Route{Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "medium"}
	return []saveStart{
		{name: "project config", configJSON: `{"model":` + savePrimary + `}`,
			load: func(p string) (config.Config, error) { return config.LoadBundle(p, "", "") },
			pick: runtimecontrol.Route{Provider: "openrouter", Model: "moonshotai/kimi-k3", Effort: "medium"}},
		{name: "built-in preset", configJSON: string(decoyJSON),
			load: func(p string) (config.Config, error) { return config.ChosenPreset(p, "openai-high") }, pick: glm},
		{name: "saved catalog preset", configJSON: saveDecoyKimi, bundles: map[string]string{"cat": `{"model":` + saveCatalog + `}`}, own: "cat",
			load: func(p string) (config.Config, error) { return config.LoadBundle(p, "", "cat") }, pick: glm},
		{name: "saved local preset", configJSON: saveDecoyLocal, bundles: map[string]string{"loc": `{"model":` + saveLocal + `}`}, own: "loc",
			load: func(p string) (config.Config, error) { return config.LoadBundle(p, "", "loc") }, pick: glm},
	}
}

// startSaveSession starts a session on start in a new project and returns
// it with the configuration it was started on.
func startSaveSession(t *testing.T, start saveStart) (*Runtime, config.Config) {
	t.Helper()
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(start.configJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, raw := range start.bundles {
		if err := os.WriteFile(config.BundlePath(project, name), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(config.BundlePath(project, "other"), []byte(saveOther), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := start.load(project)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Fallback == nil {
		t.Fatalf("%s has no fallback", start.name)
	}
	ref := testConfig("http://127.0.0.1:1")
	cfg.Finalechat, cfg.TickSeconds, cfg.Relay.Default = ref.Finalechat, ref.TickSeconds, ref.Relay.Default
	rt, err := New(cfg, Options{Project: project}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.sess.Close() })
	return rt, cfg
}

// savePreset saves the session's settings as the preset target, as the
// controller does: the settings name the target once the change is made,
// and the routes are the ones the session hands over.
func savePreset(t *testing.T, rt *Runtime, target string) config.ModelRoute {
	t.Helper()
	values := rt.activeSettings
	values.Config = "bundle:" + target
	routes, err := rt.runningRoutes(values.Routes)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := runtimecontrol.ConfigFromRoutes(rt.opts.Project, values, routes)
	if err != nil {
		t.Fatalf("saving %s: %v", target, err)
	}
	if _, err := config.SaveBundle(rt.opts.Project, target, "", cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := config.LoadBundle(rt.opts.Project, "", target)
	if err != nil {
		t.Fatal(err)
	}
	return saved.Model
}

// A preset saved from a session holds the routes the session runs, as
// configured, for every configuration it can be started on, whatever it
// runs now and wherever the preset is saved:
//   - on its first route, the chain it was started on;
//   - on its fallback, that fallback and what follows it;
//   - on a model picked from the sheet, the route that pick resolved to;
//   - after an effort-only change, its chain at that effort, and after
//     the fallback then takes over, that fallback and what follows it.
//
// Saving over the preset the session runs applies to a session started on
// a saved preset only (the project's config.json is not a preset, and a
// built-in preset's name cannot be saved over).
func TestAPresetSavedFromASessionHoldsTheRoutesItRuns(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_KEY2", "y")
	t.Setenv("OPENROUTER_API_KEY", "z")
	t.Setenv("OPENAI_API_KEY", "w")
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
	useFakeRelays(t, newFakeSeat("ok"))
	for _, start := range saveStarts(t) {
		for _, now := range []string{"primary", "fallback", "picked", "effort", "effort-then-fallback"} {
			for _, target := range []string{"new", "other", "own"} {
				if target == "own" && start.own == "" {
					continue
				}
				t.Run(start.name+"/"+now+"/"+target, func(t *testing.T) {
					rt, cfg := startSaveSession(t, start)
					want := cfg.Model
					switch now {
					case "fallback":
						moveToFallback(rt)
						want = *cfg.Model.Fallback
					case "picked":
						if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { *p = start.pick }); err != nil {
							t.Fatal(err)
						}
						var err error
						if want, err = config.PickRoute(rt.opts.Project, cfg.Model, start.pick.Provider, start.pick.Model, start.pick.Effort); err != nil {
							t.Fatal(err)
						}
						want.ReasoningEffort = start.pick.Effort
					case "effort", "effort-then-fallback":
						// Only the effort of the first route changes; its
						// chain stays. Then, in the second case, that route
						// fails and the fallback after it takes over.
						if err := pickOnFallback(rt, func(p *runtimecontrol.Route) { p.Effort = "medium" }); err != nil {
							t.Fatal(err)
						}
						want.ReasoningEffort = "medium"
						if now == "effort-then-fallback" {
							moveToFallback(rt)
							want = *cfg.Model.Fallback
						}
					}
					name := map[string]string{"new": "fresh", "other": "other", "own": start.own}[target]
					got := savePreset(t, rt, name)
					if chainText(got) != chainText(want) {
						t.Fatalf("saved:\n%sthe session runs:\n%s", chainText(got), chainText(want))
					}
				})
			}
		}
	}
}

// Two saves in one session: the first while on the fallback, the second
// after the first model was picked again. The second preset has the first
// route with its fallback, as the session was started on.
func TestASecondSaveAfterGoingBackToTheFirstRouteHasThatRoute(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_KEY2", "y")
	t.Setenv("OPENROUTER_API_KEY", "z")
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
	useFakeRelays(t, newFakeSeat("ok"))
	start := saveStarts(t)[2] // a saved catalog preset
	rt, cfg := startSaveSession(t, start)
	moveToFallback(rt)
	if got := savePreset(t, rt, "on-fallback"); chainText(got) != chainText(*cfg.Model.Fallback) {
		t.Fatalf("first save:\n%s", chainText(got))
	}
	// The settings now name the first save, as after the app's change.
	rt.activeSettings.Config = "bundle:on-fallback"
	first := cfg.Model
	if err := pickOnFallback(rt, func(p *runtimecontrol.Route) {
		*p = runtimecontrol.Route{Provider: "openrouter", Model: first.Model, Effort: first.ReasoningEffort}
	}); err != nil {
		t.Fatal(err)
	}
	if ep := rt.models[event.ActorEagent].client.Endpoint; ep.Model != first.Model || ep.APIKey != "y" {
		t.Fatalf("back on %s with key %q", ep.Model, ep.APIKey)
	}
	if got := savePreset(t, rt, "back"); chainText(got) != chainText(first) {
		t.Fatalf("second save:\n%swant:\n%s", chainText(got), chainText(first))
	}
}

// Through the app, a running session saves itself as a preset: the
// session hands the settings its routes, so the preset has its local
// model and fallback with their limits, which neither the project's
// configuration nor any catalog has.
func TestARunningSessionHandsItsRoutesToAPresetSavedFromTheApp(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
	t.Setenv("EAGENT_TEST_KEY", "x")
	useFakeRelays(t, newFakeSeat("ok"))
	model := newScripted(func(string, []map[string]any) reply { return hold() })
	t.Cleanup(model.srv.Close)
	// The session runs a saved preset of a local server with a fallback.
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	local := fmt.Sprintf(`{"model":{"protocol":"openai-chat","base_url":%q,"model":%q,"api_key_env":"EAGENT_TEST_KEY","context_tokens":50000,"max_tokens":3000,
		"fallback":{"protocol":"openai-chat","base_url":%q,"model":"eagent-fallback","api_key_env":"EAGENT_TEST_KEY","context_tokens":20000,"max_tokens":1000}}}`, model.srv.URL, testModel, model.srv.URL)
	if err := os.WriteFile(config.BundlePath(project, "box"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadBundle(project, "", "box")
	if err != nil {
		t.Fatal(err)
	}
	ref := testConfig(model.srv.URL)
	cfg.Finalechat, cfg.TickSeconds, cfg.Relay.Default = ref.Finalechat, ref.TickSeconds, ref.Relay.Default
	chain := cfg.Model
	rt, err := New(cfg, Options{Project: project, Interactive: true}, &fakeUI{input: make(chan string, 1)})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- rt.Run(runCtx) }()
	defer func() { stop(); <-done }()
	id := rt.sess.ID
	coreControlUp(t, project, id)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	q := coreRequest(t, project, id, "save-from-phone", "settings.apply",
		control.Edit{Op: "set", Key: "/tick_seconds", Value: float64(45)},
		control.Edit{Op: "set", Key: "/save_preset", Value: "copy"})
	if out, err := runtimecontrol.Deliver(ctx, project, id, q, false); err != nil || out.Status != "succeeded" {
		t.Fatalf("save: %+v %v", out, err)
	}
	saved, err := config.LoadBundle(project, "", "copy")
	if err != nil {
		t.Fatal(err)
	}
	if chainText(saved.Model) != chainText(chain) || saved.TickSeconds != 45 {
		t.Fatalf("saved (tick %d):\n%sthe session runs:\n%s", saved.TickSeconds, chainText(saved.Model), chainText(chain))
	}
}

// A pick and a save in one change: the preset is what the session runs
// once the change is applied (runningRoutes resolves the pick, as the
// swap does), for a new model and for an effort-only change, on the first
// route and on the fallback.
func TestAPickAndASaveInOneChangeSaveWhatTheSessionThenRuns(t *testing.T) {
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_KEY2", "y")
	t.Setenv("OPENROUTER_API_KEY", "z")
	t.Setenv("OPENAI_API_KEY", "w")
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_MODEL"} {
		t.Setenv(n, "")
	}
	useFakeRelays(t, newFakeSeat("ok"))
	for _, start := range saveStarts(t) {
		for _, onFallback := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fallback=%v", start.name, onFallback), func(t *testing.T) {
				rt, _ := startSaveSession(t, start)
				if onFallback {
					moveToFallback(rt)
				}
				for _, pick := range []func(*runtimecontrol.Route){
					func(p *runtimecontrol.Route) { *p = start.pick },
					func(p *runtimecontrol.Route) { p.Effort = "medium" },
				} {
					values := rt.activeSettings
					routes := map[string]runtimecontrol.Route{}
					for k, v := range values.Routes {
						routes[k] = v
					}
					p := routes[runtimecontrol.RouteEagent]
					pick(&p)
					if p == values.Routes[runtimecontrol.RouteEagent] {
						continue
					}
					routes[runtimecontrol.RouteEagent] = p
					values.Routes, values.Config = routes, "bundle:one"
					running, err := rt.runningRoutes(values.Routes)
					if err != nil {
						t.Fatal(err)
					}
					if err := pickOnFallback(rt, func(r *runtimecontrol.Route) { *r = p }); err != nil {
						t.Fatal(err)
					}
					runs := rt.models[event.ActorEagent].conf
					if chainText(running[runtimecontrol.RouteEagent]) != chainText(runs) {
						t.Fatalf("saved:\n%sruns:\n%s", chainText(running[runtimecontrol.RouteEagent]), chainText(runs))
					}
				}
			})
		}
	}
}
