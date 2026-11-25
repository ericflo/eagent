package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every preset's route must exist in the catalog with a matching protocol
// and a known effort, so the editor never shows "other model" for a
// built-in and session costs never read as unpriced.
func TestPresetsMatchCatalog(t *testing.T) {
	for _, name := range PresetNames() {
		cfg := Defaults()
		Presets[name](&cfg)
		{
			actor := "model"
			for cur := &cfg.Model; cur != nil; cur = cur.Fallback {
				m, ok := Lookup(cur.BaseURL, cur.Model)
				if !ok {
					t.Errorf("%s %s: %s at %s is not in the catalog", name, actor, cur.Model, cur.BaseURL)
					continue
				}
				if m.ProtocolFor() != cur.Protocol {
					t.Errorf("%s %s: %s protocol %s, catalog says %s", name, actor, cur.Model, cur.Protocol, m.ProtocolFor())
				}
				if cur.ReasoningEffort != "" && len(m.Efforts) > 0 && !contains(m.Efforts, cur.ReasoningEffort) {
					t.Errorf("%s %s: effort %q not in the catalog's list for %s (%v)", name, actor, cur.ReasoningEffort, cur.Model, m.Efforts)
				}
				if m.Price == nil {
					t.Errorf("%s %s: %s has no price", name, actor, cur.Model)
				}
				pr, ok := ProviderForURL(cur.BaseURL)
				if !ok || pr.KeyEnv != cur.APIKeyEnv {
					t.Errorf("%s %s: key env %s differs from the catalog's %s", name, actor, cur.APIKeyEnv, pr.KeyEnv)
				}
			}
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("preset %s does not validate: %v", name, err)
		}
	}
}

func TestCatalogIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Models {
		if _, ok := ProviderByID(m.Provider); !ok {
			t.Errorf("%s/%s: unknown provider", m.Provider, m.ID)
		}
		k := m.Provider + "|" + m.ID
		if seen[k] {
			t.Errorf("%s listed twice", k)
		}
		seen[k] = true
		for _, e := range m.Efforts {
			if EffortIndex(e) == len(EffortOrder) {
				t.Errorf("%s: effort %q is not on the canonical scale", k, e)
			}
		}
		if m.Price != nil && (m.Price.AsOf == "" || m.Price.Source == "") {
			t.Errorf("%s: price without as_of or source", k)
		}
		if m.Context == 0 {
			t.Errorf("%s: no context window", k)
		}
	}
	if _, ok := PriceFor("https://proxy.example/v1", "claude-opus-5-5"); !ok {
		t.Error("a known model id behind an unknown host should still price by id when every provider charges the same")
	}
	// The same id at different prices behind an unknown host is a guess, and
	// a guess must not wear a dollar sign.
	ambiguous := ""
	rates := map[string]Price{}
	for _, m := range Models {
		if m.Price == nil {
			continue
		}
		if p, ok := rates[m.ID]; ok && (p.In != m.Price.In || p.Out != m.Price.Out) {
			ambiguous = m.ID
			break
		}
		rates[m.ID] = *m.Price
	}
	if ambiguous != "" {
		if _, ok := PriceFor("http://gpu-box.lan:8000/v1", ambiguous); ok {
			t.Errorf("%s costs different amounts at different providers; behind an unknown host it must be unpriced", ambiguous)
		}
	}
}

func TestResolveExplainsLayersAndActive(t *testing.T) {
	project := t.TempDir()
	t.Setenv("EAGENT_PRESET", "")
	t.Setenv("EAGENT_REASONING_EFFORT", "high")
	res := Resolve(project, "", "")
	if res.LoadError != "" {
		t.Fatalf("load error: %s", res.LoadError)
	}
	if res.Active.Kind != "preset" || res.Active.Name != "glm" {
		t.Fatalf("active = %+v", res.Active)
	}
	if res.Sources["/model/model"] != "default" {
		t.Fatalf("model source = %s", res.Sources["/model/model"])
	}
	if res.Sources["/model/reasoning_effort"] != "env:EAGENT_REASONING_EFFORT" || res.Effective.Model.ReasoningEffort != "high" {
		t.Fatalf("env source = %s value=%s", res.Sources["/model/reasoning_effort"], res.Effective.Model.ReasoningEffort)
	}
	t.Setenv("EAGENT_REASONING_EFFORT", "")
	// A file overlaying a preset: the preset's values are attributed to it,
	// the file's to the file, and the file now decides the routes.
	if err := os.MkdirAll(filepath.Dir(File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(File(project), []byte(`{"preset":"anthropic-med","model":{"max_tokens":4096},"_note":"hand written","task_concurrency":3}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = Resolve(project, "", "")
	if res.LoadError != "" {
		t.Fatalf("load error: %s", res.LoadError)
	}
	if res.Sources["/model/model"] != "preset:anthropic-med" || res.Effective.Model.Model != "claude-opus-5-5" {
		t.Fatalf("preset attribution: %s %s", res.Sources["/model/model"], res.Effective.Model.Model)
	}
	if res.Sources["/model/max_tokens"] != "file" || res.Effective.Model.MaxTokens != 4096 {
		t.Fatalf("file attribution: %s", res.Sources["/model/max_tokens"])
	}
	if res.Active.Kind != "file" {
		t.Fatalf("active = %+v", res.Active)
	}
	// A legacy key is carried over, not unknown.
	if len(res.File.UnknownKeys) != 1 || res.File.UnknownKeys[0] != "_note" {
		t.Fatalf("unknown keys = %v", res.File.UnknownKeys)
	}
	if res.File.Preset != "anthropic-med" {
		t.Fatalf("file preset = %q", res.File.Preset)
	}
	// A broken file is reported, not fatal.
	if err := os.WriteFile(File(project), []byte(`{"preset": "glm",`), 0o644); err != nil {
		t.Fatal(err)
	}
	res = Resolve(project, "", "")
	if res.LoadError == "" || res.File.ParseError == "" || res.Effective.Model.Model == "" {
		t.Fatalf("broken file: err=%q parse=%q model=%q", res.LoadError, res.File.ParseError, res.Effective.Model.Model)
	}
}

func TestOverlayWritesOnlyDifferences(t *testing.T) {
	cfg := Defaults()
	Presets["anthropic-med"](&cfg)
	cfg.Model.ReasoningEffort = "high" // differs from the preset's low
	cfg.MaxCallsPerTurn = 5
	edits, err := Overlay(cfg, "anthropic-med")
	if err != nil {
		t.Fatal(err)
	}
	if string(edits["preset"]) != `"anthropic-med"` {
		t.Fatalf("preset = %s", edits["preset"])
	}
	if edits["summarizer"] != nil || edits["relay"] != nil {
		t.Fatalf("unchanged keys should be dropped: %s %s", edits["summarizer"], edits["relay"])
	}
	var model map[string]json.RawMessage
	if err := json.Unmarshal(edits["model"], &model); err != nil || len(model) != 1 || string(model["reasoning_effort"]) != `"high"` {
		t.Fatalf("model overlay = %s", edits["model"])
	}
	if string(edits["max_calls_per_turn"]) != "5" || edits["rollover_tokens"] != nil {
		t.Fatalf("scalars: %s %s", edits["max_calls_per_turn"], edits["rollover_tokens"])
	}
	cfg.Relay = Relay{Default: "codex", Profiles: map[string]RelayProfile{"mine": {Agent: "codex"}}}
	edits, _ = Overlay(cfg, "anthropic-med")
	if string(edits["relay"]) != `{"default":"codex"}` {
		t.Fatalf("relay overlay carries the default alone: %s", edits["relay"])
	}
	pin := Pin(cfg, "anthropic-med")
	var full map[string]json.RawMessage
	if err := json.Unmarshal(pin["model"], &full); err != nil || string(full["model"]) != `"claude-opus-5-5"` {
		t.Fatalf("pin should spell the model out: %s", pin["model"])
	}
	if string(pin["relay"]) != `{"default":"codex"}` {
		t.Fatalf("pin relay = %s", pin["relay"])
	}
}

func TestSaveFileKeepsUnknownKeysAndChecksETag(t *testing.T) {
	project := t.TempDir()
	// First save: no file yet, ifMatch "" means "none".
	res, err := SaveFile(project, map[string]json.RawMessage{"preset": json.RawMessage(`"glm"`), "max_calls_per_turn": json.RawMessage(`4`)}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(File(project))
	if !strings.Contains(string(raw), `"max_calls_per_turn": 4`) || res.ETag == "" {
		t.Fatalf("written: %s etag=%s", raw, res.ETag)
	}
	// A hand edit adds a key the editor does not own.
	hand := strings.Replace(string(raw), "{\n", "{\n  \"_note\": \"keep me\",\n", 1)
	if err := os.WriteFile(File(project), []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	// A stale etag is refused with the current bytes.
	_, err = SaveFile(project, map[string]json.RawMessage{"max_calls_per_turn": json.RawMessage(`2`)}, res.ETag, true)
	var conflict *ErrConflict
	if err == nil || !asConflict(err, &conflict) || !strings.Contains(conflict.Current, "keep me") {
		t.Fatalf("expected a conflict, got %v", err)
	}
	// With the right etag the edit lands, the note survives, a null deletes.
	res2, err := SaveFile(project, map[string]json.RawMessage{"max_calls_per_turn": json.RawMessage(`2`), "preset": nil}, etag([]byte(hand)), true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(File(project))
	if !strings.Contains(string(raw), "keep me") || strings.Contains(string(raw), "preset") || !strings.Contains(string(raw), `"max_calls_per_turn": 2`) {
		t.Fatalf("after save: %s", raw)
	}
	if res2.Previous != hand {
		t.Fatal("previous bytes not reported")
	}
	if bak, err := os.ReadFile(File(project) + ".bak"); err != nil || string(bak) != hand {
		t.Fatalf("backup: %v %s", err, bak)
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("file is not JSON: %v", err)
	}
}

func TestProblemsAreStructured(t *testing.T) {
	cfg := Defaults()
	cfg.TickSeconds = 0
	cfg.QuietSeconds = -1
	cfg.MaxCallsPerTurn = 33
	cfg.ReportMaxChars = 100
	cfg.RolloverTokens = 190_000 // near GLM-5.3's 200k window
	cfg.Summarizer = Summarizer{Mode: "sometimes", Model: &ModelRoute{Protocol: "openai-chat", BaseURL: "https://s.example/v1", Model: "s", APIKeyEnv: "lower-case"}}
	cfg.Relay.Default = "nobody"
	cfg.Model.Fallback = &ModelRoute{Protocol: "openai-chat", BaseURL: "https://x.example/v1", Model: "m", APIKeyEnv: "X_KEY", Fallback: &ModelRoute{Protocol: "openai-chat", BaseURL: "https://y.example/v1", Model: "m", APIKeyEnv: "Y_KEY"}}
	cfg.Model.ReasoningEffort = "max" // unchecked for GLM on Together: a warning
	codes := map[string]string{}
	for _, p := range cfg.Problems() {
		codes[p.Path] = p.Code + ":" + p.Severity
	}
	want := map[string]string{
		"/tick_seconds": "min:error", "/quiet_seconds": "min:error", "/max_calls_per_turn": "range:error", "/report_max_chars": "min:error",
		"/rollover_tokens": "near_window:warning", "/summarizer/mode": "enum:error", "/summarizer/model/api_key_env": "name:error",
		"/relay/default": "enum:error", "/model/fallback/fallback": "too_deep:error", "/model/reasoning_effort": "unchecked:warning",
	}
	for path, w := range want {
		if codes[path] != w {
			t.Errorf("%s: got %q want %q (all: %v)", path, codes[path], w, codes)
		}
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "tick_seconds") {
		t.Fatalf("Validate = %v", err)
	}
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	ok := Defaults()
	ok.Summarizer.Mode = "" // empty reads as inline
	ok.Relay = Relay{Default: "mine", Profiles: map[string]RelayProfile{"mine": {Agent: "codex"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("empty mode and a configured relay default must validate: %v", err)
	}
	ok.Relay.Default = "claude"
	if err := ok.Validate(); err != nil {
		t.Fatalf("a built-in relay default must validate: %v", err)
	}
}

func asConflict(err error, target **ErrConflict) bool {
	c, ok := err.(*ErrConflict)
	if ok {
		*target = c
	}
	return ok
}

// A bundle name is a file name, never a path: LoadBundle refuses traversal
// before any file is opened.
func TestLoadBundleRefusesPathNames(t *testing.T) {
	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "evil.json")
	if err := os.WriteFile(outside, []byte(`{"max_calls_per_turn": 9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(BundlesDir(project), strings.TrimSuffix(outside, ".json"))
	for _, name := range []string{rel, "../x", "/etc/passwd", "a/b"} {
		if _, err := LoadBundle(project, "", name); err == nil || !strings.Contains(err.Error(), "bundle names") {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := LoadBundle(project, "", "glm"); err != nil {
		t.Fatalf("a preset name is still accepted as a bundle: %v", err)
	}
}

// The web config page passes a bundle name straight from the query string;
// Resolve must confine it like LoadBundle does.
func TestResolveRefusesPathBundleNames(t *testing.T) {
	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "leak.json")
	if err := os.WriteFile(outside, []byte(`{"persona": "LEAKED", "max_calls_per_turn": 7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(BundlesDir(project), strings.TrimSuffix(outside, ".json"))
	res := Resolve(project, "", rel)
	if res.Effective.Persona == "LEAKED" || res.Effective.MaxCallsPerTurn == 7 || !strings.Contains(res.LoadError, "bundle names") || res.Active.Kind == "bundle" {
		t.Fatalf("a path was accepted as a bundle name: %+v %q", res.Active, res.LoadError)
	}
	if _, err := BundleAlone(project, "../leak"); err == nil || !strings.Contains(err.Error(), "bundle names") {
		t.Fatalf("BundleAlone: %v", err)
	}
}
