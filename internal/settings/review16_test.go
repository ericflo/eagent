package settings

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
)

// A save told to write even what the environment overrides
// (allow_shadowed), under an EAGENT_BASE_URL that moves the route to
// another server, writes the window it was given as it writes the rest,
// and reports nothing skipped. The window was put back as the file had it
// and reported skipped as the variable's.
func TestASaveOverTheEnvironmentWritesTheWindowToo(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	url := k1LocalGLM(t, project)
	t.Setenv("EAGENT_BASE_URL", url)
	t.Setenv("EAGENT_API_KEY_ENV", "")
	cfg := config.Resolve(project, "", "").Effective
	cfg.Model.ContextTokens = 64_000
	raw, _ := json.Marshal(cfg)
	res, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw, AllowShadow: true})
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Model struct {
			ContextTokens int `json:"context_tokens"`
		} `json:"model"`
	}
	written, _ := os.ReadFile(config.File(project))
	_ = json.Unmarshal(written, &file)
	if len(res.Skipped) != 0 || file.Model.ContextTokens != 64_000 {
		t.Fatalf("skipped %+v; the file's window is %d, want 64000:\n%s", res.Skipped, file.Model.ContextTokens, written)
	}
}

// r16Bundle writes a saved preset.
func r16Bundle(t *testing.T, project, name, raw string) {
	t.Helper()
	if err := os.MkdirAll(config.BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, name), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// config.json is whole only with the saved preset it names as its default
// (or the one EAGENT_CONFIG names): its relay default is a profile that
// preset defines. Loading accepts it, and so does reading the layers in
// force for a save (`eagent presets save`, `eagent config --write`, the
// page's "Save as preset"). They failed on it.
func TestTheLayersInForceIncludeTheDefaultPresetThatCompletesTheFile(t *testing.T) {
	const bundle = `{"relay":{"profiles":{"my-claude":{"agent":"claude"}}}}`
	for _, how := range []string{"default_config", "EAGENT_CONFIG"} {
		t.Run(how, func(t *testing.T) {
			clearRouteEnv(t)
			project := t.TempDir()
			file := `{"relay":{"default":"my-claude"}}`
			if how == "default_config" {
				file = `{"default_config":"mine","relay":{"default":"my-claude"}}`
			} else {
				t.Setenv("EAGENT_CONFIG", "mine")
			}
			k1WriteFile(t, project, file)
			r16Bundle(t, project, "mine", bundle)
			if _, err := config.Load(project, ""); err != nil {
				t.Fatalf("setup: the configuration does not load: %v", err)
			}
			cfg, err := CurrentLayers(project, "", "")
			if err != nil {
				t.Fatalf("the layers of a configuration that loads: %v", err)
			}
			if cfg.Relay.Default != "my-claude" {
				t.Fatalf("relay default = %q", cfg.Relay.Default)
			}
		})
	}
}

// A preset chosen from the app where a layer above the project file
// decides the preset: the saved preset default_config (or EAGENT_CONFIG)
// lays names one of its own, or EAGENT_PRESET names one. The file's
// "preset" then changes nothing that runs. The choice is refused, saying
// what stays and why, and nothing is written. (It was accepted and
// reported saved; unsetting the preset under EAGENT_PRESET also removed
// the file's own model.)
func TestAPresetPickThatAHigherLayerOverrulesIsRefused(t *testing.T) {
	relay.OfflineCatalogs()
	for _, key := range []string{"OPENAI_API_KEY", "TOGETHER_API_KEY", "ANTHROPIC_API_KEY", "DEEPINFRA_API_KEY"} {
		t.Setenv(key, "x")
	}
	const own = `"model":{"base_url":"https://api.deepinfra.com/v1/openai","api_key_env":"DEEPINFRA_API_KEY"}`
	for _, c := range []struct {
		name, file, bundle string
		env                [2]string
		edit               control.Edit
		says               string
	}{
		{"the default saved preset names a preset", `{"default_config":"mine",` + own + `}`, `{"preset":"glm"}`, [2]string{},
			control.Edit{Op: "set", Key: "/preset", Value: "openai-high"}, "the preset openai-high"},
		{"EAGENT_CONFIG's saved preset names a preset", `{` + own + `}`, `{"preset":"glm"}`, [2]string{"EAGENT_CONFIG", "mine"},
			control.Edit{Op: "set", Key: "/preset", Value: "openai-high"}, "the preset openai-high"},
		{"unset under EAGENT_PRESET", `{"preset":"openai-high",` + own + `}`, "", [2]string{"EAGENT_PRESET", "anthropic-high"},
			control.Edit{Op: "unset", Key: "/preset"}, "the defaults"},
	} {
		t.Run(c.name, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", "")
			if c.env[0] != "" {
				t.Setenv(c.env[0], c.env[1])
			}
			project := t.TempDir()
			writeProjectFile(t, project, c.file)
			if c.bundle != "" {
				r16Bundle(t, project, "mine", c.bundle)
			}
			before, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("setup: %v", err)
			}
			err = tryApply(t, &Service{Project: project}, c.edit)
			after, _ := config.Load(project, "")
			raw, _ := os.ReadFile(config.File(project))
			if err == nil || !strings.Contains(err.Error(), c.says) || !strings.Contains(err.Error(), "nothing was changed") {
				t.Errorf("got %v; want the choice refused. It runs %s @ %s (before: %s @ %s)", err, after.Model.Model, after.Model.BaseURL, before.Model.Model, before.Model.BaseURL)
			}
			if string(raw) != c.file {
				t.Errorf("the file was written: %s", raw)
			}
		})
	}
}

// config.json reaches its model at an address written into it by hand,
// with a key variable of its own. A preset is picked from the app (the
// file's own model and address go, so the preset runs whole), and the
// pick is undone. The undo puts the file back as it was, address
// included: that address and key were in force when the pick was made.
// (The undo failed for good: the address was no longer among the allowed
// choices, the pick having removed the one place it was written.)
func TestUndoingAPresetPickPutsBackTheAddressTheFileHad(t *testing.T) {
	relay.OfflineCatalogs()
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	for _, key := range []string{"OPENAI_API_KEY", "CORP_LLM_KEY"} {
		t.Setenv(key, "x")
	}
	before := `{"model":{"protocol":"openai-chat","base_url":"https://llm.corp.internal/v1","model":"corp-model","api_key_env":"CORP_LLM_KEY"}}`
	s, g, cmd, desired, inForce := rv17Setup(t, before, []control.Edit{{Op: "set", Key: "/preset", Value: "openai-high"}})
	if cfg, err := config.Load(s.Project, ""); err != nil || cfg.Model.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("setup: after the pick the model is reached at %s (%v)", cfg.Model.BaseURL, err)
	}
	_, raw, _, _, err := rv16Undo(t, s, g, cmd, before, desired, inForce)
	if err != nil {
		t.Fatalf("the undo of the pick (in force then: %v): %v", inForce, err)
	}
	values, _ := fileValues(raw)
	for key, want := range map[string]string{"/model/base_url": "https://llm.corp.internal/v1", "/model/api_key_env": "CORP_LLM_KEY", "/model/model": "corp-model"} {
		if v, _ := pointerValue(values, key); v != want {
			t.Errorf("the undo leaves %s = %v, want %s:\n%s", key, v, want, raw)
		}
	}
	if v, has := pointerValue(values, "/preset"); has {
		t.Errorf("the undo leaves the preset %v:\n%s", v, raw)
	}
}

// The summarizer is dedicated with no route of its own (it calls the main
// model's), and a connector granted preferences only sets the summarizer's
// effort. Giving the summarizer a route for that writes where it is
// reached and its key variable, which the connector may not write: the
// change is refused, saying so, and nothing is written. (The address and
// key variable were written for it, and the Undo it was offered could
// never run: it may not unset them either.)
func TestAConnectorThatMayNotWriteARouteIsNotGivenOneForTheSummarizer(t *testing.T) {
	relay.OfflineCatalogs()
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	const file = `{"summarizer":{"mode":"dedicated"}}`
	apply := func(t *testing.T, classes []string) (string, error) {
		project := t.TempDir()
		writeProjectFile(t, project, file)
		s := &Service{Project: project}
		g := s.Grant()
		g.Classes = classes
		view, err := s.RemoteSnapshot(g)
		if err != nil {
			t.Fatal(err)
		}
		p := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version,
			Edits: []control.Edit{{Op: "set", Key: "/summarizer/model/reasoning_effort", Value: "low"}}}
		_, _, err = s.Apply(context.Background(), p, g)
		raw, _ := os.ReadFile(config.File(project))
		return string(raw), err
	}
	raw, err := apply(t, []string{"preference"})
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") || raw != file {
		t.Errorf("preferences only: got %v, and the file is:\n%s", err, raw)
	}
	// With the grant a project has, the route is written whole.
	raw, err = apply(t, (&Service{}).Grant().Classes)
	values, _ := fileValues(raw)
	if v, _ := pointerValue(values, "/summarizer/model/base_url"); err != nil || v == nil {
		t.Errorf("the project's grant: got %v, and the file is:\n%s", err, raw)
	}
}

// A saved preset given as JSON that names no preset and gives its main
// route in part (another address, or another model) is laid over the
// defaults as the loader lays a file: the route takes the window where it
// now is, unless the JSON names one, and keeps the defaults' effort. It
// was saved with the defaults' 200,000.
func TestAPartialRouteWithNoPresetTakesTheWindowWhereItIs(t *testing.T) {
	clearRouteEnv(t)
	for _, c := range []struct {
		name, raw string
		want      int
	}{
		{"another server", `{"model":{"base_url":"http://127.0.0.1:9/v1","api_key_env":""}}`, 32_768},
		{"another model", `{"model":{"model":"deepseek-ai/DeepSeek-V4.1-Flash-1017"}}`, 1_048_576},
		{"a window it names", `{"model":{"base_url":"http://127.0.0.1:9/v1","api_key_env":"","context_tokens":16000}}`, 16_000},
		{"the defaults' route", `{"model":{"reasoning_effort":"low"}}`, 200_000},
	} {
		t.Run(c.name, func(t *testing.T) {
			project := t.TempDir()
			k1LocalGLM(t, project)
			cfg, err := (&Service{Project: project}).BundleConfig(BundleRequest{Name: "mine", Config: json.RawMessage(c.raw)})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Model.ContextTokens != c.want || cfg.Model.MaxTokens != config.Defaults().Model.MaxTokens {
				t.Fatalf("window %d, reply ceiling %d; want %d and the defaults' ceiling", cfg.Model.ContextTokens, cfg.Model.MaxTokens, c.want)
			}
		})
	}
}

// "Save as preset" in the web UI sends the whole configuration the form
// shows, and a field the form shows empty is not in it. A setup whose
// main route has no effort, no reply ceiling and no window named is saved
// that way, and the saved preset loads that way. It was saved with the
// defaults' "high", 32768 and 200000 in their place.
func TestSaveAsPresetSavesTheEmptyFieldsOfTheMainRouteEmpty(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeProjectFile(t, project, `{"model":{"reasoning_effort":null,"max_tokens":null,"context_tokens":null}}`)
	eff := config.Resolve(project, "", "").Effective
	if m := eff.Model; m.ReasoningEffort != "" || m.MaxTokens != 0 || m.ContextTokens != 0 {
		t.Fatalf("setup: the route in force is %+v", m)
	}
	raw, _ := json.Marshal(rv9Strip(t, eff)) // as the page sends it
	s := &Service{Project: project}
	cfg, err := s.BundleConfig(BundleRequest{Name: "mine", Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	if m := cfg.Model; m.ReasoningEffort != "" || m.MaxTokens != 0 || m.ContextTokens != 0 {
		t.Fatalf("the preset to save has effort %q, reply ceiling %d, window %d; the form showed none", m.ReasoningEffort, m.MaxTokens, m.ContextTokens)
	}
	if _, err := s.SaveBundle(context.Background(), BundleRequest{Name: "mine", Config: raw}); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadBundle(project, "", "mine")
	if err != nil {
		t.Fatal(err)
	}
	if m := got.Model; m.ReasoningEffort != "" || m.MaxTokens != 0 || m.ContextTokens != 0 {
		t.Fatalf("the saved preset loads with effort %q, reply ceiling %d, window %d", m.ReasoningEffort, m.MaxTokens, m.ContextTokens)
	}
}

// A saved preset in force gives the summarizer a route of its own and
// names no effort for it; the project file gives it none. The summarizer's
// effort is set from the app with the project's grant. The edit is of the
// route the summarizer has: the effort alone is written, the summarizer
// keeps its model, and the undo runs. (Under a preset eagent was started
// on, it was refused in words that said the summarizer has no model of its
// own; under the project's default preset, the main route was written into
// the file for it.)
func TestTheSummarizersEffortIsSavedUnderASavedPresetThatGivesItsRoute(t *testing.T) {
	relay.OfflineCatalogs()
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("TOGETHER_API_KEY", "x")
	for _, c := range []struct{ name, before, bundle string }{
		{"the preset eagent was started on", `{"tick_seconds":40}`, "mine"},
		{"the project's default preset", `{"default_config":"mine"}`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			project := t.TempDir()
			writeProjectFile(t, project, c.before)
			r16Bundle(t, project, "mine", `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3-Flash","api_key_env":"TOGETHER_API_KEY"}}}`)
			s := &Service{Project: project, Bundle: c.bundle}
			g := s.Grant()
			view, err := s.RemoteSnapshot(g)
			if err != nil {
				t.Fatal(err)
			}
			inForce := RoutePairs(config.Resolve(project, s.Preset, s.Bundle).Effective)
			cmd := control.Proposal{Operation: "settings.apply", SchemaVersion: SchemaVersion, ExpectedVersion: view.Snapshot.Version,
				Edits: []control.Edit{{Op: "set", Key: "/summarizer/model/reasoning_effort", Value: "low"}}}
			if _, _, err := s.Apply(context.Background(), cmd, g); err != nil {
				t.Fatalf("the effort of a summarizer the saved preset gives a route: %v", err)
			}
			desired, _ := os.ReadFile(config.File(project))
			cfg, err := config.Load(project, c.bundle)
			if err != nil || cfg.Summarizer.Model == nil || cfg.Summarizer.Model.ReasoningEffort != "low" || cfg.Summarizer.Model.Model != "zai-org/GLM-5.3-Flash" || cfg.Summarizer.Model.BaseURL != "https://api.together.xyz/v1" {
				t.Fatalf("after the save the summarizer is %+v (%v); the file:\n%s", cfg.Summarizer.Model, err, desired)
			}
			values, _ := fileValues(string(desired))
			if v, has := pointerValue(values, "/summarizer/model/base_url"); has {
				t.Errorf("an address was written into the file for it (%v):\n%s", v, desired)
			}
			_, raw, _, _, err := rv16Undo(t, s, g, cmd, c.before, string(desired), inForce)
			if err != nil {
				t.Fatalf("the undo: %v", err)
			}
			if values, _ := fileValues(raw); values["summarizer"] != nil {
				t.Errorf("the undo leaves the file:\n%s", raw)
			}
		})
	}
}

// The project file itself makes the summarizer dedicated and gives it no
// route (it calls the main model's), and eagent runs on a saved preset
// that gives it one. The effort written alone into the file would leave a
// file that does not load without that preset, and the whole route beside
// it writes fields the preset sets here: the change is refused, saying so,
// and nothing is written.
func TestAnEffortThatWouldLeaveTheFileNeedingItsPresetIsRefused(t *testing.T) {
	relay.OfflineCatalogs()
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("TOGETHER_API_KEY", "x")
	const before = `{"summarizer":{"mode":"dedicated"}}`
	project := t.TempDir()
	writeProjectFile(t, project, before)
	r16Bundle(t, project, "mine", `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3-Flash","api_key_env":"TOGETHER_API_KEY"}}}`)
	s := &Service{Project: project, Bundle: "mine"}
	err := tryApply(t, s, control.Edit{Op: "set", Key: "/summarizer/model/reasoning_effort", Value: "low"})
	raw, _ := os.ReadFile(config.File(project))
	if err == nil || !strings.Contains(err.Error(), "the project file gives the summarizer no whole model of its own yet") || !strings.Contains(err.Error(), "bundle:mine") || string(raw) != before {
		t.Errorf("got %v, and the file is:\n%s", err, raw)
	}
	if _, err := config.Load(project, ""); err != nil {
		t.Errorf("the file no longer loads without the preset: %v", err)
	}
}
