package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every preset's routes must exist in the catalog with a matching protocol
// and a known effort, so the editor never shows "other model" for a
// built-in and session costs never read as unpriced.
func TestPresetsMatchCatalog(t *testing.T) {
	for _, name := range PresetNames() {
		cfg := Defaults()
		Presets[name](&cfg)
		for actor, a := range map[string]Actor{"orchestrator": cfg.Orchestrator, "task": cfg.Task, "narrator": cfg.Narrator} {
			for cur := &a; cur != nil; cur = cur.Fallback {
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
	if _, ok := PriceFor("https://proxy.example/v1", "claude-opus-5"); !ok {
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
	t.Setenv("EAGENT_NARRATOR_REASONING_EFFORT", "high")
	res := Resolve(project, "", "")
	if res.LoadError != "" {
		t.Fatalf("load error: %s", res.LoadError)
	}
	if res.Active.Kind != "preset" || res.Active.Name != "glm" {
		t.Fatalf("active = %+v", res.Active)
	}
	if res.Sources["/orchestrator/model"] != "default" {
		t.Fatalf("orchestrator model source = %s", res.Sources["/orchestrator/model"])
	}
	if res.Sources["/narrator/reasoning_effort"] != "env:EAGENT_NARRATOR_REASONING_EFFORT" || res.Effective.Narrator.ReasoningEffort != "high" {
		t.Fatalf("env source = %s value=%s", res.Sources["/narrator/reasoning_effort"], res.Effective.Narrator.ReasoningEffort)
	}
	// A file overlaying a preset: the preset's values are attributed to it,
	// the file's to the file, and the file now decides the routes.
	if err := os.MkdirAll(filepath.Dir(File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(File(project), []byte(`{"preset":"anthropic-med","task":{"reasoning_effort":"medium"},"_note":"hand written"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = Resolve(project, "", "")
	if res.LoadError != "" {
		t.Fatalf("load error: %s", res.LoadError)
	}
	if res.Sources["/orchestrator/model"] != "preset:anthropic-med" || res.Effective.Orchestrator.Model != "claude-opus-5" {
		t.Fatalf("preset attribution: %s %s", res.Sources["/orchestrator/model"], res.Effective.Orchestrator.Model)
	}
	if res.Sources["/task/reasoning_effort"] != "file" || res.Effective.Task.ReasoningEffort != "medium" {
		t.Fatalf("file attribution: %s", res.Sources["/task/reasoning_effort"])
	}
	if res.Active.Kind != "file" {
		t.Fatalf("active = %+v", res.Active)
	}
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
	if res.LoadError == "" || res.File.ParseError == "" || res.Effective.Orchestrator.Model == "" {
		t.Fatalf("broken file: err=%q parse=%q model=%q", res.LoadError, res.File.ParseError, res.Effective.Orchestrator.Model)
	}
}

func TestOverlayWritesOnlyDifferences(t *testing.T) {
	cfg := Defaults()
	Presets["anthropic-med"](&cfg)
	cfg.Task.ReasoningEffort = "medium" // differs from the preset's low
	cfg.TaskConcurrency = 5
	edits, err := Overlay(cfg, "anthropic-med")
	if err != nil {
		t.Fatal(err)
	}
	if string(edits["preset"]) != `"anthropic-med"` {
		t.Fatalf("preset = %s", edits["preset"])
	}
	if edits["orchestrator"] != nil || edits["narrator"] != nil {
		t.Fatalf("unchanged actors should be dropped: %s %s", edits["orchestrator"], edits["narrator"])
	}
	var task map[string]json.RawMessage
	if err := json.Unmarshal(edits["task"], &task); err != nil || len(task) != 1 || string(task["reasoning_effort"]) != `"medium"` {
		t.Fatalf("task overlay = %s", edits["task"])
	}
	if string(edits["task_concurrency"]) != "5" || edits["rollover_tokens"] != nil {
		t.Fatalf("scalars: %s %s", edits["task_concurrency"], edits["rollover_tokens"])
	}
	pin := Pin(cfg, "anthropic-med")
	var full map[string]json.RawMessage
	if err := json.Unmarshal(pin["orchestrator"], &full); err != nil || string(full["model"]) != `"claude-opus-5"` {
		t.Fatalf("pin should spell the orchestrator out: %s", pin["orchestrator"])
	}
}

func TestSaveFileKeepsUnknownKeysAndChecksETag(t *testing.T) {
	project := t.TempDir()
	// First save: no file yet, ifMatch "" means "none".
	res, err := SaveFile(project, map[string]json.RawMessage{"preset": json.RawMessage(`"glm"`), "task_concurrency": json.RawMessage(`4`)}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(File(project))
	if !strings.Contains(string(raw), `"task_concurrency": 4`) || res.ETag == "" {
		t.Fatalf("written: %s etag=%s", raw, res.ETag)
	}
	// A hand edit adds a key the editor does not own.
	hand := strings.Replace(string(raw), "{\n", "{\n  \"_note\": \"keep me\",\n", 1)
	if err := os.WriteFile(File(project), []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	// A stale etag is refused with the current bytes.
	_, err = SaveFile(project, map[string]json.RawMessage{"task_concurrency": json.RawMessage(`2`)}, res.ETag, true)
	var conflict *ErrConflict
	if err == nil || !asConflict(err, &conflict) || !strings.Contains(conflict.Current, "keep me") {
		t.Fatalf("expected a conflict, got %v", err)
	}
	// With the right etag the edit lands, the note survives, a null deletes.
	res2, err := SaveFile(project, map[string]json.RawMessage{"task_concurrency": json.RawMessage(`2`), "preset": nil}, etag([]byte(hand)), true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(File(project))
	if !strings.Contains(string(raw), "keep me") || strings.Contains(string(raw), "preset") || !strings.Contains(string(raw), `"task_concurrency": 2`) {
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
	cfg.NarratorTickSeconds = 0
	cfg.Task.APIKeyEnv = "lower-case"
	cfg.Narrator.Fallback = &Actor{Protocol: "openai-chat", BaseURL: "https://x.example/v1", Model: "m", APIKeyEnv: "X_KEY", Fallback: &Actor{Protocol: "openai-chat", BaseURL: "https://y.example/v1", Model: "m", APIKeyEnv: "Y_KEY"}}
	cfg.Orchestrator.ReasoningEffort = "max" // unchecked for GLM on Together: a warning
	codes := map[string]string{}
	for _, p := range cfg.Problems() {
		codes[p.Path] = p.Code + ":" + p.Severity
	}
	want := map[string]string{"/narrator_tick_seconds": "min:error", "/task/api_key_env": "name:error", "/narrator/fallback/fallback": "too_deep:error", "/orchestrator/reasoning_effort": "unchecked:warning"}
	for path, w := range want {
		if codes[path] != w {
			t.Errorf("%s: got %q want %q (all: %v)", path, codes[path], w, codes)
		}
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "narrator_tick_seconds") {
		t.Fatalf("Validate = %v", err)
	}
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
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
	if err := os.WriteFile(outside, []byte(`{"task_concurrency": 9}`), 0o644); err != nil {
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
