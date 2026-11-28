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

// noEagentEnv blanks every EAGENT_* variable the test process inherited, so
// a test that loads a configuration sees only what it sets itself. (CI runs
// the suite with EAGENT_FINALECHAT=off.)
func noEagentEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "EAGENT_") {
			t.Setenv(k, "")
		}
	}
	t.Setenv("EAGENT_PRESET", "")
	t.Setenv("EAGENT_CONFIG", "")
}

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
	// The orchestrator's route is carried over as written: its effort,
	// and its fallbacks with theirs. The narrator's effort is not.
	if model.Model != "o/model" || model.BaseURL != "https://o.example/v1" || model.ReasoningEffort != "xhigh" {
		t.Fatalf("model = %+v", model)
	}
	if model.Fallback == nil || model.Fallback.Model != "o/fb" || model.Fallback.ReasoningEffort != "high" ||
		model.Fallback.Fallback == nil || model.Fallback.Fallback.Model != "o/fb2" || model.Fallback.Fallback.ReasoningEffort != "" {
		t.Fatalf("fallbacks must be carried over as written: %+v", model.Fallback)
	}
	if string(out["persona"]) != `"terse"` {
		t.Fatalf("persona = %s", out["persona"])
	}
	for _, n := range []string{"orchestrator is now model", "its reasoning effort stays xhigh, now for every call", "narrator was dropped", "task was dropped",
		"narrator.reasoning_effort (medium) is not carried over: one model has one effort, and it is the orchestrator's (xhigh)"} {
		if !hasNote(notes, n) {
			t.Errorf("no note %q in %v", n, notes)
		}
	}
}

// An effort the file names for the orchestrator is kept, its fallbacks
// keep theirs, and the note says it now holds for every call.
func TestMigrateLegacyOrchestratorAloneKeepsItsEffort(t *testing.T) {
	out, notes := MigrateLegacy(rawObject(t, `{"orchestrator": {"model": "o/model", "reasoning_effort": "xhigh", "fallback": {"model": "o/fb", "reasoning_effort": "high"}}}`))
	m := object(out["model"])
	if string(m["reasoning_effort"]) != `"xhigh"` || string(object(m["fallback"])["reasoning_effort"]) != `"high"` {
		t.Fatalf("model = %s", out["model"])
	}
	if !hasNote(notes, "orchestrator is now model") || !hasNote(notes, "its reasoning effort stays xhigh, now for every call") || hasNote(notes, "narrator.reasoning_effort") {
		t.Fatalf("notes = %v", notes)
	}
	project := t.TempDir()
	writeConfig(t, project, `{"orchestrator": {"model": "custom/model", "reasoning_effort": "xhigh"}}`)
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Model != "custom/model" || cfg.Model.ReasoningEffort != "xhigh" {
		t.Fatalf("model = %+v", cfg.Model)
	}
	// With no effort named anywhere, none is written: the preset's holds.
	out, notes = MigrateLegacy(rawObject(t, `{"orchestrator": {"model": "o/model"}, "narrator": {"model": "n/model"}}`))
	if _, ok := object(out["model"])["reasoning_effort"]; ok || !hasNote(notes, "orchestrator is now model") {
		t.Fatalf("model = %s notes %v", out["model"], notes)
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
	for _, d := range legacyDropped {
		in += `, "` + d.key + `": 1`
	}
	out, notes := MigrateLegacy(rawObject(t, in+"}"))
	if len(out) != 1 || string(out["persona"]) != `"x"` {
		t.Fatalf("out = %v", out)
	}
	for _, d := range legacyDropped {
		if !hasNote(notes, d.key+" was dropped: "+d.why) {
			t.Errorf("no note for %s: %v", d.key, notes)
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
	if hasNote(notes, "orchestrator is now model") || !hasNote(notes, "orchestrator was ignored: the file also has model, which is used") {
		t.Fatalf("notes = %v", notes)
	}
}

// A project config.json written for the three-model design loads end to end.
func TestLoadLegacyProjectFile(t *testing.T) {
	project := t.TempDir()
	noEagentEnv(t)
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
	want.Model.ReasoningEffort = "xhigh" // the orchestrator's, as the file wrote it
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
			"config.json: narrator.reasoning_effort (medium) is not carried over: one model has one effort, and it is the orchestrator's (xhigh)",
		} {
			if !hasNote(notes, n) {
				t.Errorf("%s: no note %q in %v", name, n, notes)
			}
		}
		// The file names rollover_tokens, so the changed default is not news.
		if hasNote(notes, "rollover_tokens") {
			t.Errorf("%s: rollover note for a file that names it: %v", name, notes)
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
	if err := os.WriteFile(BundlePath(project, "old"), []byte(`{"narrator": {"reasoning_effort": "low"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadBundle(project, "", "old")
	// The narrator's effort alone is not carried over: the effort stays the
	// preset's.
	if err != nil || cfg.Model.ReasoningEffort != want.Model.ReasoningEffort || cfg.Model.Model != "claude-fable-5-1" || !hasNote(cfg.Migrated, "old.json: narrator.reasoning_effort (low) is not carried over") {
		t.Fatalf("bundle: %v %+v %v", err, cfg.Model, cfg.Migrated)
	}
	alone, err := BundleAlone(project, "old")
	if err != nil || alone.Model.ReasoningEffort == "low" {
		t.Fatalf("BundleAlone: %v %+v", err, alone.Model)
	}
}

// The editor works on the migrated file and its first save writes today's
// keys only, keeping keys it does not own and relay profiles as written.
func TestEditorRewritesLegacyFile(t *testing.T) {
	project := t.TempDir()
	noEagentEnv(t)
	writeConfig(t, project, `{
  "orchestrator": {"model": "custom/model", "reasoning_effort": "low"},
  "narrator": {"reasoning_effort": "low"},
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
	if len(after.Migrated) != 0 || after.Model.Model != "custom/model" || after.Model.ReasoningEffort != "low" || after.TickSeconds != 20 || after.QuietSeconds != 240 || after.Persona != "brief" || after.Relay.Default != "mine" || after.Relay.Profiles["mine"].Agent != "codex" {
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
	noEagentEnv(t)
	set := map[string]string{
		"EAGENT_MODEL":                       "env/model",
		"EAGENT_ORCHESTRATOR_MODEL":          "ignored/model",
		"EAGENT_ORCHESTRATOR_MAX_TOKENS":     "4096",
		"EAGENT_NARRATOR_REASONING_EFFORT":   "xhigh", // no longer read: the one model has one effort, EAGENT_REASONING_EFFORT
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
	if cfg.Model.Model != "env/model" || cfg.Model.MaxTokens != 4096 || cfg.Model.ReasoningEffort != "high" || cfg.Model.APIKeyEnv != "" ||
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

// One effort for every call: the rules for an older file's two efforts.
func TestMigrateLegacyEffortRules(t *testing.T) {
	const narratorAlone = "narrator.reasoning_effort (low) is not carried over: the one model does the orchestrator's work too, at the effort the orchestrator ran at. Set model.reasoning_effort to choose another level."
	for _, c := range []struct {
		name, in, effort, note string // effort "" means none is written
	}{
		{"both, equal", `{"orchestrator": {"model": "m", "reasoning_effort": "high"}, "narrator": {"reasoning_effort": "high"}}`, "high", "its reasoning effort stays high, now for every call"},
		{"both, different", `{"orchestrator": {"model": "m", "reasoning_effort": "xhigh"}, "narrator": {"reasoning_effort": "low"}}`, "xhigh",
			"narrator.reasoning_effort (low) is not carried over: one model has one effort, and it is the orchestrator's (xhigh). Set model.reasoning_effort to choose another level."},
		{"orchestrator alone", `{"orchestrator": {"model": "m", "reasoning_effort": "xhigh"}, "narrator": {"model": "n"}}`, "xhigh", "its reasoning effort stays xhigh, now for every call"},
		{"narrator alone", `{"orchestrator": {"model": "z-ai/glm-5.3"}, "narrator": {"reasoning_effort": "low"}}`, "", narratorAlone},
		{"narrator alone, no orchestrator", `{"narrator": {"reasoning_effort": "low"}}`, "", narratorAlone},
		{"neither", `{"orchestrator": {"model": "m"}, "narrator": {"model": "n"}}`, "", "orchestrator is now model"},
	} {
		out, notes := MigrateLegacy(rawObject(t, c.in))
		model := object(out["model"])
		got := ""
		if raw, ok := model["reasoning_effort"]; ok {
			got = strings.Trim(string(raw), `"`)
		}
		if got != c.effort {
			t.Errorf("%s: effort = %q, want %q (model %s)", c.name, got, c.effort, out["model"])
		}
		if !hasNote(notes, c.note) {
			t.Errorf("%s: no note %q in %v", c.name, c.note, notes)
		}
	}
	// End to end: the narrator's low never becomes the effort of every call;
	// the preset's applies, which is the level its orchestrator ran at.
	project := t.TempDir()
	noEagentEnv(t)
	writeConfig(t, project, `{"orchestrator": {"model": "z-ai/glm-5.3"}, "narrator": {"reasoning_effort": "low"}}`)
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.ReasoningEffort != Defaults().Model.ReasoningEffort || !hasNote(cfg.Migrated, "config.json: "+narratorAlone) {
		t.Fatalf("effort = %q notes %v", cfg.Model.ReasoningEffort, cfg.Migrated)
	}
}

// Every key of an older file that is dropped or changes meaning says so.
func TestMigrateLegacyNotesEveryChange(t *testing.T) {
	_, notes := MigrateLegacy(rawObject(t, `{"orchestrator": {"model": "m"}, "narrator": {}, "task": {}, "task_concurrency": 3, "max_task_turns": 9,
		"bash_wait_seconds": 1, "bash_timeout_seconds": 2, "tool_output_max_chars": 3, "allow_outside_project": true, "max_orchestrator_calls_per_turn": 40}`))
	for _, want := range []string{
		"narrator was dropped: one model does the talking now",
		"task was dropped: there are no task workers now",
		"task_concurrency was dropped: there are no task workers now",
		"max_task_turns was dropped: there are no task workers now",
		"bash_wait_seconds was dropped: eagent has no shell tool now",
		"bash_timeout_seconds was dropped: eagent has no shell tool now",
		"tool_output_max_chars was dropped: eagent has no shell or file tools now",
		"allow_outside_project was dropped: eagent has no file tools now",
		"max_orchestrator_calls_per_turn is now max_calls_per_turn, and it is now where a turn stops (it was only a reminder)",
		"rollover_tokens is not set in this file, so the conversation now restarts at half the model's context window where the default was 150000 (300000 in some presets); set rollover_tokens to fix a number",
	} {
		if !hasNote(notes, want) {
			t.Errorf("no note %q in %v", want, notes)
		}
	}
	for _, n := range notes {
		if strings.Contains(n, "\n") {
			t.Errorf("a note is more than one line: %q", n)
		}
	}
	// A file that names rollover_tokens keeps it and hears nothing about it;
	// a current file gets no notes at all.
	if _, notes := MigrateLegacy(rawObject(t, `{"task": {}, "rollover_tokens": 150000}`)); hasNote(notes, "rollover_tokens") {
		t.Errorf("rollover note for a file that names it: %v", notes)
	}
	if _, notes := MigrateLegacy(rawObject(t, `{"model": {"model": "m"}}`)); notes != nil {
		t.Errorf("notes for a current file: %v", notes)
	}
}

// An old reminder threshold above what max_calls_per_turn allows must not
// keep eagent from starting; the new key itself is still checked.
func TestMigrateLegacyClampsCallsPerTurn(t *testing.T) {
	project := t.TempDir()
	noEagentEnv(t)
	writeConfig(t, project, `{"max_orchestrator_calls_per_turn": 500, "rollover_tokens": 150000}`)
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxCallsPerTurn != 200 || !hasNote(cfg.Migrated, "max_orchestrator_calls_per_turn (500) is above 200, the most max_calls_per_turn allows, so it is 200") {
		t.Fatalf("max_calls_per_turn = %d notes %v", cfg.MaxCallsPerTurn, cfg.Migrated)
	}
	writeConfig(t, project, `{"max_orchestrator_calls_per_turn": 45}`)
	if cfg, err := Load(project, ""); err != nil || cfg.MaxCallsPerTurn != 45 || hasNote(cfg.Migrated, "is above 200") {
		t.Fatalf("45: %v %d %v", err, cfg.MaxCallsPerTurn, cfg.Migrated)
	}
	writeConfig(t, project, `{"max_calls_per_turn": 500}`)
	if _, err := Load(project, ""); err == nil || !strings.Contains(err.Error(), "max_calls_per_turn must be between 1 and 200") {
		t.Fatalf("max_calls_per_turn 500 loaded: %v", err)
	}
	// Today's key wins over the old one, which is noted as ignored.
	out, notes := MigrateLegacy(rawObject(t, `{"max_calls_per_turn": 20, "max_orchestrator_calls_per_turn": 500}`))
	if string(out["max_calls_per_turn"]) != "20" || !hasNote(notes, "max_orchestrator_calls_per_turn was ignored: the file also has max_calls_per_turn") {
		t.Fatalf("out %v notes %v", out, notes)
	}
}

// The orchestrator's effort variable is a fallback for EAGENT_REASONING_EFFORT,
// and every variable of the three-actor design that does nothing now is
// named, never with its value.
func TestLegacyEnvVars(t *testing.T) {
	project := t.TempDir()
	noEagentEnv(t)
	t.Setenv("EAGENT_REASONING_EFFORT", "")
	t.Setenv("EAGENT_ORCHESTRATOR_REASONING_EFFORT", "xhigh")
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.ReasoningEffort != "xhigh" || EnvOverrides()["/model/reasoning_effort"] != "EAGENT_ORCHESTRATOR_REASONING_EFFORT" {
		t.Fatalf("effort = %q source %q", cfg.Model.ReasoningEffort, EnvOverrides()["/model/reasoning_effort"])
	}
	t.Setenv("EAGENT_REASONING_EFFORT", "low")
	if cfg, _ := Load(project, ""); cfg.Model.ReasoningEffort != "low" || EnvOverrides()["/model/reasoning_effort"] != "EAGENT_REASONING_EFFORT" {
		t.Fatalf("EAGENT_REASONING_EFFORT must win: %q", cfg.Model.ReasoningEffort)
	}

	const secret = "value-that-must-not-appear"
	var retired []string
	for _, prefix := range []string{"EAGENT_NARRATOR_", "EAGENT_TASK_"} {
		for _, suffix := range []string{"MODEL", "PROTOCOL", "BASE_URL", "API_KEY_ENV", "REASONING_EFFORT", "MAX_TOKENS", "REPLAY_REASONING"} {
			retired = append(retired, prefix+suffix)
		}
	}
	retired = append(retired, "EAGENT_TASK_CONCURRENCY")
	for _, name := range retired {
		t.Setenv(name, secret)
	}
	// Still read: not noted.
	t.Setenv("EAGENT_NARRATOR_TICK_SECONDS", "15")
	t.Setenv("EAGENT_NARRATOR_QUIET_SECONDS", "60")
	cfg, err = Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range retired {
		if !hasNote(cfg.Migrated, name+" is set but no longer read") {
			t.Errorf("no note naming %s", name)
		}
	}
	for _, n := range cfg.Migrated {
		if strings.Contains(n, secret) {
			t.Errorf("a note carries a variable's value (note withheld)")
		}
		if strings.Contains(n, "TICK_SECONDS") || strings.Contains(n, "QUIET_SECONDS") {
			t.Errorf("a variable still read is noted: %s", strings.SplitN(n, " ", 2)[0])
		}
	}
	if len(cfg.Migrated) != len(retired) {
		t.Errorf("%d notes for %d variables", len(cfg.Migrated), len(retired))
	}
	// An empty key variable meant "no key needed", so it counts as set; an
	// empty model did nothing, so it does not.
	for _, name := range retired {
		t.Setenv(name, "")
	}
	cfg, _ = Load(project, "")
	if !hasNote(cfg.Migrated, "EAGENT_NARRATOR_API_KEY_ENV is set") || hasNote(cfg.Migrated, "EAGENT_NARRATOR_MODEL") {
		t.Errorf("empty variables: %d notes", len(cfg.Migrated))
	}
}

// Project instructions: INSTRUCTIONS.md, else the project's AGENTS.md, as
// in the three-actor design.
func TestLoadInstructionsFallsBackToAgentsMD(t *testing.T) {
	project := t.TempDir()
	noEagentEnv(t)
	if cfg, err := Load(project, ""); err != nil || cfg.Instructions != "" {
		t.Fatalf("none: %v %q", err, cfg.Instructions)
	}
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("\n  run make test  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := Load(project, ""); cfg.Instructions != "run make test" {
		t.Fatalf("AGENTS.md: %q", cfg.Instructions)
	}
	own := filepath.Join(project, ".agents", "eagent", "INSTRUCTIONS.md")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := Load(project, ""); cfg.Instructions != "run make test" {
		t.Fatalf("an empty INSTRUCTIONS.md must fall through: %q", cfg.Instructions)
	}
	if err := os.WriteFile(own, []byte("use the staging cluster"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := Load(project, ""); cfg.Instructions != "use the staging cluster" {
		t.Fatalf("INSTRUCTIONS.md: %q", cfg.Instructions)
	}
	if res := Resolve(project, "", ""); res.Effective.Instructions != "use the staging cluster" {
		t.Fatalf("Resolve: %q", res.Effective.Instructions)
	}
}
