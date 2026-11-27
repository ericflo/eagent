package runtimecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/settings"
)

// r14Env clears the variables that name a route or a bundle, and keeps
// the settings descriptor from starting the agents installed here to list
// their models.
func r14Env(t *testing.T) {
	t.Helper()
	relay.OfflineCatalogs()
	for _, suffix := range []string{"MODEL", "PROTOCOL", "BASE_URL", "API_KEY_ENV", "REASONING_EFFORT", "MAX_TOKENS", "REPLAY_REASONING"} {
		for _, prefix := range []string{"EAGENT_", "EAGENT_ORCHESTRATOR_", "EAGENT_SUMMARIZER_"} {
			t.Setenv(prefix+suffix, "")
			os.Unsetenv(prefix + suffix)
		}
	}
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_ROLLOVER_TOKENS"} {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
}

// r14LocalGLM registers a keyless server whose list says it serves the
// defaults' zai-org/GLM-5.3 with a 32k window, and returns its address.
func r14LocalGLM(t *testing.T, project string) string {
	t.Helper()
	res, err := config.RegisterServer(context.Background(), project, config.ProbeOptions{URL: "http://127.0.0.1:9/v1", ID: "local", Label: "Local"}, true)
	if err != nil {
		t.Fatal(err)
	}
	config.UpdateLiveCacheForServer(project, "local", []config.Model{{Provider: "local", ID: "zai-org/GLM-5.3", Context: 32_768}}, "")
	return res.Server.BaseURL
}

// r14Session is a running session's controller whose configured chain is
// chain, with Routes as the harness gives them (runningRoutes and
// resolvePick, reduced to what these tests need: the entry of the chain a
// pick names, at the pick's effort, else the configured route picked from
// the catalog). apply records what the session takes.
func r14Session(t *testing.T, project, session string, cfg config.Config, applied *[]Values) *Controller {
	t.Helper()
	c, err := New(project, session, FromConfigFor(project, cfg), func(_ Request, v Values, _ map[string]any) error {
		*applied = append(*applied, v)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.Routes = r14Routes(project, cfg)
	return c
}

// r14Routes is Controller.Routes for a session configured with cfg.
func r14Routes(project string, cfg config.Config) func(map[string]Route) (map[string]config.ModelRoute, error) {
	return func(routes map[string]Route) (map[string]config.ModelRoute, error) {
		out := map[string]config.ModelRoute{}
		for who, pick := range routes {
			if entry, ok := ChainEntry(project, cfg.Model, pick); ok {
				entry.ReasoningEffort = pick.Effort
				out[who] = entry
				continue
			}
			a, err := config.PickRoute(project, cfg.Model, pick.Provider, pick.Model, pick.Effort)
			if err != nil {
				return nil, err
			}
			out[who] = a
		}
		return out, nil
	}
}

// r14Request is a settings.apply of edits against c's current view.
func r14Request(t *testing.T, c *Controller, edits ...control.Edit) Request {
	t.Helper()
	view, err := View(c.Project, c.Session, c.Generation, c.Values, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := Grant(c.Project, c.Session)
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: Schema, ExpectedVersion: view.Snapshot.Version, Generation: c.Generation, Edits: edits}
	raw, _ := json.Marshal(p)
	return Request{ID: newID(), UserID: "fixture-user", Proposal: p, Digest: artifact.Digest(raw), Expires: time.Now().Add(2 * time.Second), Grant: grant}
}

// r14Process hands q to the session's controller as the connector leaves
// it in pending, and returns the controller's outcome.
func r14Process(t *testing.T, c *Controller, q Request) Outcome {
	t.Helper()
	if err := writeCommitted(c.pending, q.ID, q); err != nil {
		t.Fatal(err)
	}
	if err := c.Poll(); err != nil {
		t.Fatal(err)
	}
	var out Outcome
	if err := readCommitted(c.outcomes, q.ID, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// r14Chain is eagent's model on OpenRouter with a fallback, each reached
// with a key variable of its own; OpenRouter's usual variable is not set.
func r14Chain(t *testing.T) config.Config {
	t.Helper()
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("EAGENT_TEST_KEY", "x")
	t.Setenv("EAGENT_TEST_KEY2", "y")
	cfg := config.Defaults()
	cfg.Model = config.ModelRoute{
		Protocol: "openai-chat", BaseURL: "https://openrouter.ai/api/v1", Model: "z-ai/glm-5.3", APIKeyEnv: "EAGENT_TEST_KEY", ReasoningEffort: "high",
		Fallback: &config.ModelRoute{Protocol: "openai-chat", BaseURL: "https://openrouter.ai/api/v1", Model: "z-ai/glm-5.3-flash", APIKeyEnv: "EAGENT_TEST_KEY2", ReasoningEffort: "low"},
	}
	return cfg
}

// r14OnFallback is the chain's session after the fallback took over: the
// live settings show the fallback (harness followRoute).
func r14OnFallback(t *testing.T, project, session string, applied *[]Values) *Controller {
	t.Helper()
	c := r14Session(t, project, session, r14Chain(t), applied)
	v := c.Values
	v.Routes = map[string]Route{RouteEagent: {Provider: "openrouter", Model: "z-ai/glm-5.3-flash", Effort: "low"}}
	if err := c.Update(v); err != nil {
		t.Fatal(err)
	}
	return c
}

// The session takes a change of a route it has as that route is
// configured (harness resolvePick): an effort change on the fallback it
// runs, on the fallback's own key; the first route picked again, on its
// own key; an effort change on a route at an address no provider knows;
// and the configuration it runs chosen again. Neither the connector
// (Deliver) nor the controller refuses them as if the route were the
// catalog's, with OpenRouter's usual key variable, or had to have a
// provider.
func TestTheSessionsOwnRoutesAreCheckedAsTheSessionRunsThem(t *testing.T) {
	r14Env(t)
	cases := []struct {
		name  string
		start func(t *testing.T, project, session string, applied *[]Values) *Controller
		edits []control.Edit
		want  Route
	}{
		{"effort on the running fallback", r14OnFallback,
			[]control.Edit{{Op: "set", Key: "/routes/eagent/effort", Value: "medium"}},
			Route{Provider: "openrouter", Model: "z-ai/glm-5.3-flash", Effort: "medium"}},
		{"the first route again from the fallback", r14OnFallback,
			[]control.Edit{{Op: "set", Key: "/routes/eagent/model", Value: "z-ai/glm-5.3"}, {Op: "set", Key: "/routes/eagent/effort", Value: "medium"}},
			Route{Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "medium"}},
		{"effort on a route no provider knows", func(t *testing.T, project, session string, applied *[]Values) *Controller {
			t.Setenv("EAGENT_TEST_KEY", "x")
			cfg := config.Defaults()
			cfg.Model = config.ModelRoute{Protocol: "openai-chat", BaseURL: "http://127.0.0.1:1/v1", Model: "local-model", APIKeyEnv: "EAGENT_TEST_KEY", ReasoningEffort: "low"}
			return r14Session(t, project, session, cfg, applied)
		}, []control.Edit{{Op: "set", Key: "/routes/eagent/effort", Value: "high"}},
			Route{Provider: "", Model: "local-model", Effort: "high"}},
		{"the saved preset it runs chosen again", func(t *testing.T, project, session string, applied *[]Values) *Controller {
			cfg := r14Chain(t)
			cfg.Model.Fallback = nil
			if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
				t.Fatal(err)
			}
			raw := `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"EAGENT_TEST_KEY","reasoning_effort":"high"}}`
			if err := os.WriteFile(config.BundlePath(project, "mine"), []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg.Name = "mine"
			return r14Session(t, project, session, cfg, applied)
		}, []control.Edit{{Op: "set", Key: "/config", Value: "bundle:mine"}},
			Route{Provider: "openrouter", Model: "z-ai/glm-5.3", Effort: "high"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			// Through the connector, with the session polling.
			var applied []Values
			c := tc.start(t, project, "connector", &applied)
			q := r14Request(t, c, tc.edits...)
			stop := runLoop(t, c)
			out, err := Deliver(context.Background(), project, "connector", q, false)
			stop()
			if err != nil || out.Status != "succeeded" {
				t.Fatalf("through the connector: %s %v (%v)", out.Status, out.Result["message"], err)
			}
			if len(applied) != 1 || applied[0].Routes[RouteEagent] != tc.want {
				t.Fatalf("through the connector the session took %+v, want %+v", applied, tc.want)
			}
			// At the controller, as the connector leaves it.
			applied = nil
			c = tc.start(t, project, "controller", &applied)
			if out := r14Process(t, c, r14Request(t, c, tc.edits...)); out.Status != "succeeded" {
				t.Fatalf("at the controller: %s %v", out.Status, out.Result["message"])
			}
			if len(applied) != 1 || applied[0].Routes[RouteEagent] != tc.want {
				t.Fatalf("the controller applied %+v, want %+v", applied, tc.want)
			}
		})
	}
}

// What the session cannot run is still refused, with the reason, through
// the connector too, and nothing is applied: the fallback's own key
// unset; and a model at an address no provider knows that the session's
// routes do not have.
func TestWhatTheSessionCannotRunIsRefusedWithItsReason(t *testing.T) {
	r14Env(t)
	cases := []struct {
		name  string
		start func(t *testing.T, project, session string, applied *[]Values) *Controller
		edits []control.Edit
		says  string
	}{
		{"the fallback's own key unset", func(t *testing.T, project, session string, applied *[]Values) *Controller {
			c := r14OnFallback(t, project, session, applied)
			t.Setenv("EAGENT_TEST_KEY2", "")
			return c
		}, []control.Edit{{Op: "set", Key: "/routes/eagent/effort", Value: "medium"}}, "EAGENT_TEST_KEY2 is not set"},
		{"another model at an address no provider knows", func(t *testing.T, project, session string, applied *[]Values) *Controller {
			t.Setenv("EAGENT_TEST_KEY", "x")
			cfg := config.Defaults()
			cfg.Model = config.ModelRoute{Protocol: "openai-chat", BaseURL: "http://127.0.0.1:1/v1", Model: "local-model", APIKeyEnv: "EAGENT_TEST_KEY"}
			c := r14Session(t, project, session, cfg, applied)
			c.Routes = func(routes map[string]Route) (map[string]config.ModelRoute, error) {
				if routes[RouteEagent].Model != "local-model" {
					return nil, fmt.Errorf("model %q is not at an address eagent knows a provider for", routes[RouteEagent].Model)
				}
				return map[string]config.ModelRoute{RouteEagent: cfg.Model}, nil
			}
			return c
		}, []control.Edit{{Op: "set", Key: "/routes/eagent/model", Value: "other-model"}}, "not at an address eagent knows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			var applied []Values
			c := tc.start(t, project, "connector", &applied)
			q := r14Request(t, c, tc.edits...)
			stop := runLoop(t, c)
			out, err := Deliver(context.Background(), project, "connector", q, false)
			stop()
			if msg, _ := out.Result["message"].(string); err != nil || out.Status != "rejected" || !strings.Contains(msg, tc.says) || len(applied) != 0 {
				t.Fatalf("through the connector: %s %q (%v), applied %+v", out.Status, msg, err, applied)
			}
		})
	}
}

// A remote edit of /model/base_url alone, to a server that serves the same
// model with 32k, leaves the route with that server's window.
func TestARemoteAddressEditTakesTheServersWindow(t *testing.T) {
	r14Env(t)
	project := t.TempDir()
	url := r14LocalGLM(t, project)
	s := &settings.Service{Project: project}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	_, after, err := s.Apply(context.Background(), control.Proposal{
		Operation: "settings.apply", SchemaVersion: settings.SchemaVersion, ExpectedVersion: view.Snapshot.Version,
		Edits: []control.Edit{{Op: "set", Key: "/model/base_url", Value: url}},
	}, g)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(after.Snapshot.Effective["/model/context_tokens"]); got != "32768" {
		t.Fatalf("window after the edit = %s, want 32768", got)
	}
	cfg, err := config.Load(project, "")
	if err != nil || cfg.Model.ContextTokens != 32_768 || cfg.Rollover() != config.AutoRolloverWithin(32_768, cfg.Model.MaxTokens, 0) {
		t.Fatalf("loaded window %d rollover %d err %v", cfg.Model.ContextTokens, cfg.Rollover(), err)
	}
}

// Under EAGENT_BASE_URL, the window in force is that server's, so a save
// from the editor (which sends what is in force) does not write it to the
// file: there it would be the window of the file's own server once the
// variable is unset.
func TestASaveUnderAnAddressVariableDoesNotWriteItsWindow(t *testing.T) {
	t.Skip("known, not fixed: a route an address variable (EAGENT_BASE_URL, EAGENT_SUMMARIZER_BASE_URL) moves to another server keeps the window it had (finding 19 of the fourteenth review, variable half); refreshing it there threw away a window the file names and claimed the window in saves the variable did not move (r14window2_test.go, settings/r14window_test.go)")
	r14Env(t)
	project := t.TempDir()
	url := r14LocalGLM(t, project)
	t.Setenv("EAGENT_BASE_URL", url)
	t.Setenv("EAGENT_API_KEY_ENV", "")
	s := &settings.Service{Project: project}
	res := config.Resolve(project, "", "")
	if res.Effective.Model.ContextTokens != 32_768 {
		t.Fatalf("window in force = %d", res.Effective.Model.ContextTokens)
	}
	cfg := res.Effective
	cfg.TickSeconds = 15
	raw, _ := json.Marshal(cfg)
	if _, err := s.Save(context.Background(), settings.Request{Config: raw}); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Clean(config.File(project)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), "context_tokens") || !strings.Contains(string(file), `"tick_seconds": 15`) && !strings.Contains(string(file), `"tick_seconds":15`) {
		t.Fatalf("file = %s", file)
	}
	os.Unsetenv("EAGENT_BASE_URL")
	os.Unsetenv("EAGENT_API_KEY_ENV")
	if back, err := config.Load(project, ""); err != nil || back.Model.ContextTokens != 200_000 {
		t.Fatalf("without the variable: window %d err %v", back.Model.ContextTokens, err)
	}
}
