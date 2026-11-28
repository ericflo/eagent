package settings

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
)

// r17Case is a project file, the saved preset "mine" beside it (when
// given), a registered server (when servers), shell variables, and a
// proposal applied with a grant of classes (the project's own when none
// are given).
type r17Case struct {
	name, file, bundle string
	servers            bool
	env                map[string]string
	classes            []string
	edits              []control.Edit
}

// r17Apply sets a case up and applies its proposal. It returns the
// project and how the apply went.
func r17Apply(t *testing.T, c r17Case) (string, error) {
	t.Helper()
	relay.OfflineCatalogs()
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	for _, key := range []string{"OPENAI_API_KEY", "TOGETHER_API_KEY", "ANTHROPIC_API_KEY", "DEEPINFRA_API_KEY"} {
		t.Setenv(key, "x")
	}
	for k, v := range c.env {
		t.Setenv(k, v)
	}
	project := t.TempDir()
	writeProjectFile(t, project, c.file)
	if c.bundle != "" {
		r16Bundle(t, project, "mine", c.bundle)
	}
	if c.servers {
		if err := os.WriteFile(config.ServersPath(project), []byte(`[{"id":"box","label":"box","base_url":"http://127.0.0.1:9","protocol":"openai-chat"}]`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{Project: project}
	g := s.Grant()
	if c.classes != nil {
		g.Classes = c.classes
	}
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version, Edits: c.edits}
	_, _, err = s.Apply(context.Background(), p, g)
	return project, err
}

const r17DeepInfra = `"model":{"protocol":"openai-chat","base_url":"https://api.deepinfra.com/v1/openai","model":"zai-org/GLM-5.2","api_key_env":"DEEPINFRA_API_KEY"}`

// What the settings page and the app send when a preset is chosen: the
// preset set and, beside it, the file's own model unset (the model the
// form then shows is the preset's). An unset gives the route no model of
// the proposal's own, so the choice is a preset pick like the preset set
// alone: the file's address goes with its model, and where a part that
// stays would run the preset's model somewhere else, the choice is
// refused and nothing is written. (It was taken for a pick of a model:
// accepted, with the preset's model sent to the file's old provider.)
func TestAPresetChosenWithTheFilesModelUnsetIsAPresetPick(t *testing.T) {
	pick := []control.Edit{{Op: "set", Key: "/preset", Value: "openai-high"}, {Op: "unset", Key: "/model/model"}}
	for _, c := range []struct {
		r17Case
		says string // "" when the choice runs whole and is written
	}{
		{r17Case{name: "the file reaches its model at another provider", file: `{` + r17DeepInfra + `}`, edits: pick}, ""},
		{r17Case{name: "a grant that may not write the address", file: `{` + r17DeepInfra + `}`, classes: []string{"preference"}, edits: pick}, "base_url stays https://api.deepinfra.com/v1/openai"},
		{r17Case{name: "a variable sets the address", file: `{"model":{"model":"zai-org/GLM-5.2"}}`,
			env: map[string]string{"EAGENT_BASE_URL": "https://api.together.xyz/v1", "EAGENT_API_KEY_ENV": "TOGETHER_API_KEY"}, edits: pick}, "EAGENT_BASE_URL sets it"},
		{r17Case{name: "the default saved preset names a preset", file: `{"default_config":"mine","model":{"model":"zai-org/GLM-5.2"}}`, bundle: `{"preset":"glm"}`, edits: pick}, `the saved preset "mine" sets it`},
		{r17Case{name: "beside a change of the servers", file: `{"model":{"model":"zai-org/GLM-5.2"}}`, servers: true,
			env:   map[string]string{"EAGENT_BASE_URL": "https://api.together.xyz/v1"},
			edits: append([]control.Edit{{Op: "unset", Key: "/servers"}}, pick...)}, "EAGENT_BASE_URL sets it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			project, err := r17Apply(t, c.r17Case)
			raw, _ := os.ReadFile(config.File(project))
			cfg, _ := config.Load(project, "")
			if c.says == "" {
				if err != nil || cfg.Model.Model != "gpt-6-astra" || cfg.Model.BaseURL != "https://api.openai.com/v1" || cfg.Model.APIKeyEnv != "OPENAI_API_KEY" {
					t.Fatalf("got %v; it runs %s @ %s with $%s, want the preset whole:\n%s", err, cfg.Model.Model, cfg.Model.BaseURL, cfg.Model.APIKeyEnv, raw)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.says) || !strings.Contains(err.Error(), "nothing was changed") {
				t.Errorf("got %v; want the choice refused. It runs %s @ %s (%s, $%s)", err, cfg.Model.Model, cfg.Model.BaseURL, cfg.Model.Protocol, cfg.Model.APIKeyEnv)
			}
			if string(raw) != c.file {
				t.Errorf("the file was written: %s", raw)
			}
			if got, _ := config.LoadServers(project); c.servers && len(got) != 1 {
				t.Errorf("the refusal says nothing was changed, and the registered servers went from 1 to %d", len(got))
			}
		})
	}
}

// A preset is chosen that a layer above the file already names: the saved
// preset the file takes as its default, or EAGENT_PRESET. The file itself
// named none, so for the file it is a choice like any other: the file's
// own address goes, so the preset runs whole, and where a layer above
// keeps another model running the choice is refused. (Compared with the
// preset in force, it was no change, and nothing of a pick was done:
// accepted, with the preset's model sent to the file's old provider.)
func TestAPresetALayerAboveTheFileAlreadyNamesIsStillAPick(t *testing.T) {
	const own = `"model":{"base_url":"https://api.deepinfra.com/v1/openai","api_key_env":"DEEPINFRA_API_KEY"}`
	set := func(preset string) []control.Edit { return []control.Edit{{Op: "set", Key: "/preset", Value: preset}} }
	for _, c := range []struct {
		r17Case
		model, at, says string
	}{
		{r17Case{name: "with the default saved preset unset beside it", file: `{"default_config":"mine",` + own + `}`, bundle: `{"preset":"openai-high"}`,
			edits: append([]control.Edit{{Op: "unset", Key: "/default_config"}}, set("openai-high")...)}, "gpt-6-astra", "https://api.openai.com/v1", ""},
		{r17Case{name: "the default saved preset names it", file: `{"default_config":"mine",` + own + `}`, bundle: `{"preset":"glm"}`, edits: set("glm")},
			"zai-org/GLM-5.3", "https://api.together.xyz/v1", ""},
		{r17Case{name: "EAGENT_PRESET names it", file: `{` + own + `}`, env: map[string]string{"EAGENT_PRESET": "openai-high"}, edits: set("openai-high")},
			"gpt-6-astra", "https://api.openai.com/v1", ""},
		{r17Case{name: "the default saved preset names it and another model", file: `{"default_config":"mine"}`,
			bundle: `{"preset":"openai-high","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"deepseek-ai/DeepSeek-V4.1-Flash-1017","api_key_env":"TOGETHER_API_KEY"}}`,
			edits:  set("openai-high")}, "", "", `the saved preset "mine" sets it`},
	} {
		t.Run(c.name, func(t *testing.T) {
			project, err := r17Apply(t, c.r17Case)
			raw, _ := os.ReadFile(config.File(project))
			cfg, _ := config.Load(project, "")
			if c.says != "" {
				if err == nil || !strings.Contains(err.Error(), c.says) || !strings.Contains(err.Error(), "nothing was changed") || string(raw) != c.file {
					t.Errorf("got %v; want the choice refused. It runs %s @ %s, and the file is:\n%s", err, cfg.Model.Model, cfg.Model.BaseURL, raw)
				}
				return
			}
			if err != nil || cfg.Model.Model != c.model || cfg.Model.BaseURL != c.at {
				t.Errorf("got %v; it runs %s @ %s with $%s, want %s @ %s:\n%s", err, cfg.Model.Model, cfg.Model.BaseURL, cfg.Model.APIKeyEnv, c.model, c.at, raw)
			}
		})
	}
}

// r17Flash is a saved preset that gives the summarizer a model of its own.
const r17Flash = `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3-Flash","api_key_env":"TOGETHER_API_KEY"}}}`

// The saved preset the project file takes as its default gives the
// summarizer its model, and the summarizer's effort was set from the app:
// the file holds that effort alone. Read without the preset (a save from
// the local page, `eagent servers use`, the default preset unset from the
// app) the summarizer is inline and calls nothing, and the effort left
// there names no address: there is none to refuse. (Each of them failed
// with "summarizer:  is not a catalog provider or registered server".)
func TestAnEffortLeftForASummarizerThatCallsNothingFailsNoLaterSave(t *testing.T) {
	set := func(t *testing.T) *Service {
		project, err := r17Apply(t, r17Case{file: `{"default_config":"mine"}`, bundle: r17Flash,
			edits: []control.Edit{{Op: "set", Key: "/summarizer/model/reasoning_effort", Value: "low"}}})
		if err != nil {
			t.Fatalf("setup: the effort was refused: %v", err)
		}
		return &Service{Project: project}
	}
	t.Run("a save from the local page", func(t *testing.T) {
		s := set(t)
		cfg := config.Resolve(s.Project, "", "").Effective
		cfg.TickSeconds = 45
		raw, _ := json.Marshal(cfg)
		if _, err := s.Save(context.Background(), Request{Config: raw}); err != nil {
			t.Fatalf("the save: %v", err)
		}
		if got, err := config.Load(s.Project, ""); err != nil || got.TickSeconds != 45 || got.Summarizer.Model == nil || got.Summarizer.Model.ReasoningEffort != "low" || got.Summarizer.Model.Model != "zai-org/GLM-5.3-Flash" {
			t.Fatalf("after the save: tick %d, summarizer %+v, %v", got.TickSeconds, got.Summarizer.Model, err)
		}
	})
	t.Run("the default preset unset from the app", func(t *testing.T) {
		s := set(t)
		if err := tryApply(t, s, control.Edit{Op: "unset", Key: "/default_config"}); err != nil {
			t.Fatalf("the unset: %v", err)
		}
		if _, err := config.Load(s.Project, ""); err != nil {
			t.Fatalf("after the unset the file does not load: %v", err)
		}
	})
}

// The summarizer's mode and its effort are changed from the app,
// together and one after the other, over each shape the project file can
// have for it, under a saved preset in force each way one can be (the
// flag, EAGENT_CONFIG, the file's default) and under none. Whatever is
// accepted leaves a file that loads with the preset, still loads without
// it where it did before, can still be edited from the app, and keeps the
// summarizer on the model it had (none of these changes names another).
// A refusal writes nothing.
func TestSummarizerChangesFromTheAppLeaveAFileThatStillStandsAlone(t *testing.T) {
	effort := control.Edit{Op: "set", Key: "/summarizer/model/reasoning_effort", Value: "low"}
	dedicated := control.Edit{Op: "set", Key: "/summarizer/mode", Value: "dedicated"}
	type steps = [][]control.Edit
	// Under the flag, a preset that sets the mode locks it, and every
	// change of it is refused before any of this: there the preset gives
	// the model only.
	for _, preset := range []struct {
		name, raw string
		hows      []string
	}{
		{"none", "", []string{"no preset"}},
		{"a preset that makes it dedicated with a model", r17Flash, []string{"EAGENT_CONFIG", "default_config"}},
		{"a preset that gives it a model only", strings.Replace(r17Flash, `"mode":"dedicated",`, "", 1), []string{"flag"}},
	} {
		for _, how := range preset.hows {
			for _, file := range []struct{ name, body string }{
				{"nothing for it", `"tick_seconds":40`},
				{"dedicated", `"summarizer":{"mode":"dedicated"}`},
				{"an effort alone", `"summarizer":{"model":{"reasoning_effort":"medium"}}`},
			} {
				for _, seq := range []struct {
					name  string
					steps steps
				}{
					{"both at once", steps{{dedicated, effort}}},
					{"the effort, then dedicated", steps{{effort}, {dedicated}}},
				} {
					t.Run(strings.Join([]string{preset.name, how, file.name, seq.name}, "/"), func(t *testing.T) {
						relay.OfflineCatalogs()
						clearRouteEnv(t)
						t.Setenv("EAGENT_FINALECHAT", "")
						t.Setenv("TOGETHER_API_KEY", "x")
						project := t.TempDir()
						body := file.body
						if how == "default_config" {
							body = `"default_config":"mine",` + body
						}
						writeProjectFile(t, project, "{"+body+"}")
						s := &Service{Project: project}
						if preset.raw != "" {
							r16Bundle(t, project, "mine", preset.raw)
						}
						switch how {
						case "flag":
							s.Bundle = "mine"
						case "EAGENT_CONFIG":
							t.Setenv("EAGENT_CONFIG", "mine")
						}
						// The file read with no saved preset at all.
						alone := func() error {
							raw, _ := os.ReadFile(config.File(project))
							var m map[string]json.RawMessage
							_ = json.Unmarshal(raw, &m)
							delete(m, "default_config")
							raw, _ = json.Marshal(m)
							bare := t.TempDir()
							writeProjectFile(t, bare, string(raw))
							if how == "EAGENT_CONFIG" {
								os.Unsetenv("EAGENT_CONFIG")
								defer os.Setenv("EAGENT_CONFIG", "mine")
							}
							_, err := config.Load(bare, "")
							return err
						}
						stood := alone() == nil
						was, err := config.Load(project, s.Bundle)
						if err != nil {
							t.Fatalf("setup: %v", err)
						}
						calls := was.Summarizer.Route(was.Model)
						for i, edits := range seq.steps {
							before, _ := os.ReadFile(config.File(project))
							err := tryApply(t, s, edits...)
							raw, _ := os.ReadFile(config.File(project))
							if err != nil {
								if string(raw) != string(before) {
									t.Fatalf("step %d was refused (%v) and the file was written:\n%s", i+1, err, raw)
								}
								t.Logf("step %d refused: %v", i+1, err)
								return
							}
							now, err := config.Load(project, s.Bundle)
							if err != nil {
								t.Fatalf("after step %d the file does not load with what is in force: %v\n%s", i+1, err, raw)
							}
							if err := alone(); stood && err != nil {
								t.Fatalf("after step %d the file no longer loads without the saved preset: %v\n%s", i+1, err, raw)
							}
							if _, err := s.RemoteSnapshot(s.Grant()); err != nil {
								t.Fatalf("after step %d the settings can no longer be read from the app: %v\n%s", i+1, err, raw)
							}
							if got := now.Summarizer.Route(now.Model); got.Model != calls.Model || got.BaseURL != calls.BaseURL {
								t.Fatalf("after step %d the summarizer's model went from %s @ %s to %s @ %s:\n%s", i+1, calls.Model, calls.BaseURL, got.Model, got.BaseURL, raw)
							}
						}
					})
				}
			}
		}
	}
}

// What the rule above leaves alone, and what it refuses.
func TestSummarizerChangesLeaveARouteTheFileAlreadyHasAsWritten(t *testing.T) {
	const whole = `"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3-Flash","api_key_env":"TOGETHER_API_KEY"}}`
	effort := control.Edit{Op: "set", Key: "/summarizer/model/reasoning_effort", Value: "low"}
	route := func(t *testing.T, project string) map[string]any {
		t.Helper()
		raw, _ := os.ReadFile(config.File(project))
		values, _ := fileValues(string(raw))
		v, _ := pointerValue(values, "/summarizer/model")
		m, _ := v.(map[string]any)
		return m
	}
	// A whole route gains the field that was set and nothing else: no
	// effort, window or reply ceiling of the main model's is written
	// beside it.
	t.Run("a whole route gains only what was set", func(t *testing.T) {
		project, err := r17Apply(t, r17Case{file: `{` + whole + `}`, edits: []control.Edit{effort}})
		if got := route(t, project); err != nil || len(got) != 5 || got["reasoning_effort"] != "low" {
			t.Fatalf("got %v, and the file's route is %v", err, got)
		}
	})
	// A file that already needed its saved preset (written by hand) is
	// edited as before: the rule is for what a change would newly break.
	t.Run("a file that already needed its preset", func(t *testing.T) {
		const file = `{"summarizer":{"mode":"dedicated","model":{"reasoning_effort":"medium"}}}`
		relay.OfflineCatalogs()
		clearRouteEnv(t)
		t.Setenv("EAGENT_FINALECHAT", "")
		t.Setenv("TOGETHER_API_KEY", "x")
		project := t.TempDir()
		writeProjectFile(t, project, file)
		r16Bundle(t, project, "mine", r17Flash)
		s := &Service{Project: project, Bundle: "mine"}
		if err := tryApply(t, s, control.Edit{Op: "set", Key: "/tick_seconds", Value: float64(45)}); err != nil {
			t.Fatalf("another setting: %v", err)
		}
		if err := tryApply(t, s, effort); err != nil {
			t.Fatalf("the effort: %v", err)
		}
		if got := route(t, project); len(got) != 1 || got["reasoning_effort"] != "low" {
			t.Fatalf("the file's route is %v", got)
		}
	})
	// The file's route is whole, a saved preset in force gives the
	// summarizer a model too, and the file's own model is unset from the
	// app. The file would load only with that preset: refused.
	t.Run("a field the route needs is unset", func(t *testing.T) {
		project, err := r17Apply(t, r17Case{file: `{` + whole + `}`, bundle: r17Flash, env: map[string]string{"EAGENT_CONFIG": "mine"},
			edits: []control.Edit{{Op: "unset", Key: "/summarizer/model/model"}}})
		raw, _ := os.ReadFile(config.File(project))
		if err == nil || !strings.Contains(err.Error(), "nothing was changed") || string(raw) != `{`+whole+`}` {
			t.Fatalf("got %v, and the file is:\n%s", err, raw)
		}
	})
}

// EAGENT_MODEL names the model the route already has, so the form shows
// the model as the file's to change. A model with a larger window is
// picked there and saved. The variable still decides the model, so the
// pick is not written, and is reported skipped; nor is the window that
// came with it. (The window was written beside the model that stayed:
// GLM-5.3 loaded with a window of 1,048,576 and would restart its
// conversation at 524,000.)
func TestAPickAModelVariableKeepsOutLeavesNoWindowBehind(t *testing.T) {
	for _, c := range []struct{ name, variable, ptr, file string }{
		{"the main model", "EAGENT_MODEL", "/model", `{}`},
		{"the summarizer's", "EAGENT_SUMMARIZER_MODEL", "/summarizer/model",
			`{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}}`},
	} {
		for _, form := range []string{"says what it was loaded with", "says nothing of it"} {
			t.Run(c.name+"/the form "+form, func(t *testing.T) {
				relay.OfflineCatalogs()
				clearRouteEnv(t)
				t.Setenv("EAGENT_FINALECHAT", "")
				t.Setenv(c.variable, "zai-org/GLM-5.3")
				project := t.TempDir()
				writeProjectFile(t, project, c.file)
				route := func(cfg *config.Config) *config.ModelRoute {
					if c.ptr == "/model" {
						return &cfg.Model
					}
					return cfg.Summarizer.Model
				}
				cfg := config.Resolve(project, "", "").Effective
				window := route(&cfg).ContextTokens
				req := Request{}
				if form == "says what it was loaded with" {
					req.Loaded, _ = json.Marshal(cfg)
				}
				route(&cfg).Model, route(&cfg).ContextTokens = "deepseek-ai/DeepSeek-V4.1-Flash-1017", 1_048_576
				req.Config, _ = json.Marshal(cfg)
				res, err := (&Service{Project: project}).Save(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := os.ReadFile(config.File(project))
				got, err := config.Load(project, "")
				if err != nil || route(&got).Model != "zai-org/GLM-5.3" || route(&got).ContextTokens != window {
					t.Errorf("it runs %s with a window of %d (%v), want zai-org/GLM-5.3 with %d:\n%s", route(&got).Model, route(&got).ContextTokens, err, window, raw)
				}
				// (A field the file has is reported kept as the file has it.)
				for _, leaf := range []string{"/model", "/context_tokens"} {
					if f := (EnvField{c.ptr + leaf, c.variable}); !slices.Contains(res.Skipped, f) && !slices.Contains(res.Kept, f) {
						t.Errorf("%s was not reported as left to %s: skipped %+v, kept %+v", f.Pointer, c.variable, res.Skipped, res.Kept)
					}
				}
			})
		}
	}
}
