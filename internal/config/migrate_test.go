package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func rawObject(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
	return m
}

func canonString(raw json.RawMessage) string { return string(canon(raw)) }

func hasNote(notes []string, part string) bool {
	for _, n := range notes {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}

func TestMigrateLegacyOrchestratorAndNarrator(t *testing.T) {
	in := rawObject(t, `{
		"orchestrator": {"model": "o/model", "base_url": "https://o.example/v1", "reasoning_effort": "xhigh",
			"fallback": {"model": "o/fb", "reasoning_effort": "high", "fallback": {"model": "o/fb2"}}},
		"narrator": {"model": "n/model", "reasoning_effort": "medium"},
		"task": {"model": "t/model"},
		"persona": "terse"
	}`)
	before := canonString(mustMarshal(t, in))
	out, notes := MigrateLegacy(in)
	if canonString(mustMarshal(t, in)) != before {
		t.Fatal("the input was changed")
	}
	for _, k := range []string{"orchestrator", "narrator", "task"} {
		if _, ok := out[k]; ok {
			t.Errorf("%s survived: %s", k, out[k])
		}
	}
	var model ModelRoute
	if err := json.Unmarshal(out["model"], &model); err != nil {
		t.Fatal(err)
	}
	if model.Model != "o/model" || model.BaseURL != "https://o.example/v1" || model.ReasoningEffort != "medium" {
		t.Fatalf("model = %+v", model)
	}
	if model.Fallback == nil || model.Fallback.Model != "o/fb" || model.Fallback.ReasoningEffort != "medium" ||
		model.Fallback.Fallback == nil || model.Fallback.Fallback.ReasoningEffort != "medium" {
		t.Fatalf("fallbacks must run at the narrator's effort: %+v", model.Fallback)
	}
	if string(out["persona"]) != `"terse"` {
		t.Fatalf("persona = %s", out["persona"])
	}
	for _, n := range []string{"orchestrator is now model", "narrator was dropped", "task was dropped"} {
		if !hasNote(notes, n) {
			t.Errorf("no note %q in %v", n, notes)
		}
	}
}

func TestMigrateLegacyOrchestratorAloneRunsLow(t *testing.T) {
	out, notes := MigrateLegacy(rawObject(t, `{"orchestrator": {"model": "o/model", "reasoning_effort": "xhigh", "fallback": {"model": "o/fb", "reasoning_effort": "xhigh"}}}`))
	m := object(out["model"])
	if _, ok := m["reasoning_effort"]; ok {
		t.Fatalf("the orchestrator's effort carried over: %s", out["model"])
	}
	if string(object(m["fallback"])["reasoning_effort"]) != `"low"` {
		t.Fatalf("fallback effort = %s", m["fallback"])
	}
	if len(notes) != 1 || !hasNote(notes, "orchestrator is now model") {
		t.Fatalf("notes = %v", notes)
	}
	// Loaded, the model takes the defaults' low effort.
	project := t.TempDir()
	writeConfig(t, project, `{"orchestrator": {"model": "custom/model", "reasoning_effort": "xhigh"}}`)
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Model != "custom/model" || cfg.Model.ReasoningEffort != "low" {
		t.Fatalf("model = %+v", cfg.Model)
	}
}

func TestMigrateLegacyRenamesAndQuietSeconds(t *testing.T) {
	out, notes := MigrateLegacy(rawObject(t, `{"narrator_tick_seconds": 45, "narrator_quiet_seconds": 90}`))
	if string(out["tick_seconds"]) != "45" || string(out["quiet_seconds"]) != "90" || len(out) != 2 {
		t.Fatalf("renamed = %v", out)
	}
	if !hasNote(notes, "narrator_tick_seconds is now tick_seconds") || !hasNote(notes, "narrator_quiet_seconds is now quiet_seconds") {
		t.Fatalf("notes = %v", notes)
	}
	// relay.quiet_seconds wins over narrator_quiet_seconds, and the rest of
	// relay stays.
	out, notes = MigrateLegacy(rawObject(t, `{"narrator_quiet_seconds": 90, "relay": {"quiet_seconds": 300, "default": "codex", "profiles": {"x": {"agent": "codex"}}}}`))
	if string(out["quiet_seconds"]) != "300" || !hasNote(notes, "relay.quiet_seconds is now quiet_seconds") {
		t.Fatalf("quiet_seconds = %s notes %v", out["quiet_seconds"], notes)
	}
	relay := object(out["relay"])
	if _, ok := relay["quiet_seconds"]; ok || string(relay["default"]) != `"codex"` || object(relay["profiles"])["x"] == nil {
		t.Fatalf("relay = %s", out["relay"])
	}
	// A relay left empty goes; today's quiet_seconds is never overwritten.
	out, _ = MigrateLegacy(rawObject(t, `{"quiet_seconds": 60, "narrator_quiet_seconds": 90, "relay": {"quiet_seconds": 300}}`))
	if string(out["quiet_seconds"]) != "60" {
		t.Fatalf("quiet_seconds = %s", out["quiet_seconds"])
	}
	if _, ok := out["relay"]; ok {
		t.Fatalf("empty relay kept: %s", out["relay"])
	}
}

func TestMigrateLegacyDropsKeys(t *testing.T) {
	in := `{"persona": "x"`
	for _, k := range legacyDropped {
		in += `, "` + k + `": 1`
	}
	out, notes := MigrateLegacy(rawObject(t, in+"}"))
	if len(out) != 1 || string(out["persona"]) != `"x"` {
		t.Fatalf("out = %v", out)
	}
	for _, k := range legacyDropped {
		if !hasNote(notes, k+" was dropped") {
			t.Errorf("no note for %s: %v", k, notes)
		}
	}
}

func TestMigrateLegacyLeavesCurrentObjectsAlone(t *testing.T) {
	in := rawObject(t, `{"model": {"model": "m"}, "tick_seconds": 40, "relay": {"default": "codex"}, "_note": "x"}`)
	out, notes := MigrateLegacy(in)
	if notes != nil || !reflect.DeepEqual(out, in) {
		t.Fatalf("changed: %v %v", out, notes)
	}
	if HasLegacyKeys(in) {
		t.Fatal("HasLegacyKeys on a current object")
	}
	// With both model and orchestrator, model is kept as written.
	out, notes = MigrateLegacy(rawObject(t, `{"model": {"model": "new"}, "orchestrator": {"model": "old"}}`))
	if canonString(out["model"]) != `{"model":"new"}` || out["orchestrator"] != nil || len(out) != 1 {
		t.Fatalf("out = %v", out)
	}
	if hasNote(notes, "orchestrator is now model") {
		t.Fatalf("notes = %v", notes)
	}
}

// A project config.json written for the three-model design loads end to end.
func TestLoadLegacyProjectFile(t *testing.T) {
	project := t.TempDir()
	t.Setenv("EAGENT_PRESET", "")
	t.Setenv("EAGENT_CONFIG", "")
	legacy := `{
  "preset": "anthropic-med",
  "orchestrator": {"model": "claude-fable-5-1", "reasoning_effort": "xhigh"},
  "task": {"reasoning_effort": "high"},
  "narrator": {"reasoning_effort": "medium"},
  "task_concurrency": 3,
  "max_task_turns": 40,
  "narrator_tick_seconds": 20,
  "narrator_quiet_seconds": 120,
  "rollover_tokens": 90000,
  "allow_outside_project": true,
  "relay": {"quiet_seconds": 240, "default": "claude"},
  "persona": "brief"
}`
	writeConfig(t, project, legacy)
	want := Defaults()
	Presets["anthropic-med"](&want)
	want.Preset = "anthropic-med"
	want.Model.Model = "claude-fable-5-1"
	want.Model.ReasoningEffort = "medium"
	want.TickSeconds = 20
	want.QuietSeconds = 240
	want.RolloverTokens = 90000
	want.Relay.Default = "claude"
	want.Persona = "brief"
	check := func(name string, cfg Config, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		notes := cfg.Migrated
		cfg.Migrated = nil
		if !reflect.DeepEqual(cfg, want) {
			t.Fatalf("%s:\n got %+v\nwant %+v", name, cfg, want)
		}
		for _, n := range []string{
			"config.json: orchestrator is now model",
			"config.json: narrator was dropped",
			"config.json: narrator_tick_seconds is now tick_seconds",
			"config.json: relay.quiet_seconds is now quiet_seconds",
			"config.json: task was dropped",
			"config.json: task_concurrency was dropped",
			"config.json: max_task_turns was dropped",
			"config.json: allow_outside_project was dropped",
		} {
			if !hasNote(notes, n) {
				t.Errorf("%s: no note %q in %v", name, n, notes)
			}
		}
	}
	cfg, err := Load(project, "")
	check("Load", cfg, err)
	cfg, err = LoadBundleWithFile(project, "", "", rawObject(t, legacy))
	check("LoadBundleWithFile", cfg, err)

	// A legacy bundle migrates too, with notes naming its file.
	if err := os.MkdirAll(BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BundlePath(project, "old"), []byte(`{"narrator": {"reasoning_effort": "high"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadBundle(project, "", "old")
	if err != nil || cfg.Model.ReasoningEffort != "high" || cfg.Model.Model != "claude-fable-5-1" || !hasNote(cfg.Migrated, "old.json: the narrator's reasoning effort is now model.reasoning_effort") {
		t.Fatalf("bundle: %v %+v %v", err, cfg.Model, cfg.Migrated)
	}
	alone, err := BundleAlone(project, "old")
	if err != nil || alone.Model.ReasoningEffort != "high" {
		t.Fatalf("BundleAlone: %v %+v", err, alone.Model)
	}
}

// The editor works on the migrated file and its first save writes today's
// keys only, keeping keys it does not own and relay profiles as written.
func TestEditorRewritesLegacyFile(t *testing.T) {
	project := t.TempDir()
	t.Setenv("EAGENT_PRESET", "")
	t.Setenv("EAGENT_CONFIG", "")
	writeConfig(t, project, `{
  "orchestrator": {"model": "custom/model"},
  "narrator": {"reasoning_effort": "medium"},
  "narrator_tick_seconds": 20,
  "task_concurrency": 3,
  "relay": {"quiet_seconds": 240, "profiles": {"mine": {"agent": "codex", "args": ["--x"]}}},
  "_note": "keep me"
}`)
	res := Resolve(project, "", "")
	if res.LoadError != "" {
		t.Fatal(res.LoadError)
	}
	if len(res.File.UnknownKeys) != 1 || res.File.UnknownKeys[0] != "_note" {
		t.Fatalf("unknown keys = %v", res.File.UnknownKeys)
	}
	if res.Sources["/model/model"] != "file" || res.Sources["/model/reasoning_effort"] != "file" || res.Sources["/tick_seconds"] != "file" || res.Active.Kind != "file" {
		t.Fatalf("sources = %v active %+v", res.Sources, res.Active)
	}
	cfg := res.Effective
	cfg.Relay.Default = "mine"
	cfg.Persona = "brief"
	edits, err := Overlay(cfg, res.Preset)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveFile(project, edits, res.File.ETag, true); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(File(project))
	got := rawObject(t, string(raw))
	if HasLegacyKeys(got) {
		t.Fatalf("legacy keys survived the save:\n%s", raw)
	}
	if string(got["_note"]) != `"keep me"` {
		t.Fatalf("_note lost:\n%s", raw)
	}
	relay := object(got["relay"])
	if string(relay["default"]) != `"mine"` || canonString(relay["profiles"]) != `{"mine":{"agent":"codex","args":["--x"]}}` {
		t.Fatalf("relay = %s", got["relay"])
	}
	after, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Migrated) != 0 || after.Model.Model != "custom/model" || after.Model.ReasoningEffort != "medium" || after.TickSeconds != 20 || after.QuietSeconds != 240 || after.Persona != "brief" || after.Relay.Default != "mine" || after.Relay.Profiles["mine"].Agent != "codex" {
		t.Fatalf("after save: %+v\n%s", after, raw)
	}
	// Clearing the default keeps the profiles; with nothing left relay goes.
	if _, err := SaveFile(project, map[string]json.RawMessage{"relay": nil}, "", false); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(File(project))
	relay = object(rawObject(t, string(raw))["relay"])
	if _, ok := relay["default"]; ok || relay["profiles"] == nil {
		t.Fatalf("relay after clearing the default = %v", relay)
	}
	writeConfig(t, project, `{"relay": {"default": "codex"}}`)
	if _, err := SaveFile(project, map[string]json.RawMessage{"relay": nil}, "", false); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(File(project))
	if _, ok := rawObject(t, string(raw))["relay"]; ok {
		t.Fatalf("empty relay kept:\n%s", raw)
	}
}

func TestEnvOverridesNewAndLegacyNames(t *testing.T) {
	project := t.TempDir()
	t.Setenv("EAGENT_PRESET", "")
	t.Setenv("EAGENT_CONFIG", "")
	set := map[string]string{
		"EAGENT_MODEL":                       "env/model",
		"EAGENT_ORCHESTRATOR_MODEL":          "ignored/model",
		"EAGENT_ORCHESTRATOR_MAX_TOKENS":     "4096",
		"EAGENT_NARRATOR_REASONING_EFFORT":   "medium",
		"EAGENT_API_KEY_ENV":                 "",
		"EAGENT_SUMMARIZER_MODE":             "dedicated",
		"EAGENT_SUMMARIZER_MODEL":            "small/model",
		"EAGENT_SUMMARIZER_REASONING_EFFORT": "none",
		"EAGENT_NARRATOR_TICK_SECONDS":       "15",
		"EAGENT_QUIET_SECONDS":               "0",
		"EAGENT_ROLLOVER_TOKENS":             "50000",
		"EAGENT_PERSONA":                     "chatty",
		"EAGENT_DEFAULT_RELAY":               "codex",
		"EAGENT_FINALECHAT":                  "off",
	}
	for k, v := range set {
		t.Setenv(k, v)
	}
	want := map[string]string{
		"/model/model":                       "EAGENT_MODEL",
		"/model/max_tokens":                  "EAGENT_ORCHESTRATOR_MAX_TOKENS",
		"/model/reasoning_effort":            "EAGENT_NARRATOR_REASONING_EFFORT",
		"/model/api_key_env":                 "EAGENT_API_KEY_ENV",
		"/summarizer/mode":                   "EAGENT_SUMMARIZER_MODE",
		"/summarizer/model/model":            "EAGENT_SUMMARIZER_MODEL",
		"/summarizer/model/reasoning_effort": "EAGENT_SUMMARIZER_REASONING_EFFORT",
		"/tick_seconds":                      "EAGENT_NARRATOR_TICK_SECONDS",
		"/quiet_seconds":                     "EAGENT_QUIET_SECONDS",
		"/rollover_tokens":                   "EAGENT_ROLLOVER_TOKENS",
		"/persona":                           "EAGENT_PERSONA",
		"/relay/default":                     "EAGENT_DEFAULT_RELAY",
		"/finalechat/enabled":                "EAGENT_FINALECHAT",
	}
	if got := EnvOverrides(); !reflect.DeepEqual(got, want) {
		t.Fatalf("EnvOverrides:\n got %v\nwant %v", got, want)
	}
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	sum := cfg.Summarizer.Route(cfg.Model)
	if cfg.Model.Model != "env/model" || cfg.Model.MaxTokens != 4096 || cfg.Model.ReasoningEffort != "medium" || cfg.Model.APIKeyEnv != "" ||
		cfg.Summarizer.Mode != SummarizerDedicated || sum.Model != "small/model" || sum.ReasoningEffort != "none" || sum.BaseURL != cfg.Model.BaseURL ||
		cfg.TickSeconds != 15 || cfg.QuietSeconds != 0 || cfg.RolloverTokens != 50000 || cfg.Persona != "chatty" || cfg.Relay.Default != "codex" || cfg.Finalechat.Wanted() {
		t.Fatalf("loaded = %+v summarizer %+v", cfg, sum)
	}
	res := Resolve(project, "", "")
	for ptr, name := range want {
		if res.Sources[ptr] != "env:"+name {
			t.Errorf("source of %s = %q, want env:%s", ptr, res.Sources[ptr], name)
		}
	}
	if res.Sources["/summarizer/model/base_url"] != "env:EAGENT_SUMMARIZER_MODEL" {
		t.Errorf("copied summarizer field source = %q", res.Sources["/summarizer/model/base_url"])
	}
	// The current name wins over the legacy one when both are set.
	t.Setenv("EAGENT_TICK_SECONDS", "25")
	if got := EnvOverrides()["/tick_seconds"]; got != "EAGENT_TICK_SECONDS" {
		t.Fatalf("tick source = %s", got)
	}
	if cfg, _ := Load(project, ""); cfg.TickSeconds != 25 {
		t.Fatalf("tick = %d", cfg.TickSeconds)
	}
}

func writeConfig(t *testing.T, project, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(File(project), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
