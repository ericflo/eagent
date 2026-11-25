package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// loadedFlat is what a project with this file loads to, as JSON pointers,
// without the notes about older keys.
func loadedFlat(t *testing.T, project, bundle string) map[string]string {
	t.Helper()
	cfg, err := LoadBundle(project, "", bundle)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return flatten(cfg)
}

// Files eagent wrote before (from main, and from this branch before zero
// values were spelled out) load to exactly what they loaded to then. The
// loaded values are kept beside each sample, as written by the code that
// wrote it.
func TestFilesWrittenBeforeKeepLoadingToTheSameValues(t *testing.T) {
	update := os.Getenv("EAGENT_UPDATE_WRITTEN") != ""
	noEagentEnv(t)
	samples, err := filepath.Glob("testdata/written/*.json")
	if err != nil || len(samples) == 0 {
		t.Fatalf("no samples: %v", err)
	}
	for _, sample := range samples {
		if strings.HasSuffix(sample, ".loaded.json") {
			continue
		}
		raw, err := os.ReadFile(sample)
		if err != nil {
			t.Fatal(err)
		}
		project, bundle := t.TempDir(), ""
		if strings.Contains(sample, "-bundle-") {
			bundle = "mine"
			if err := os.MkdirAll(BundlesDir(project), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(BundlePath(project, bundle), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			writeConfig(t, project, string(raw))
		}
		got := loadedFlat(t, project, bundle)
		golden := strings.TrimSuffix(sample, ".json") + ".loaded.json"
		if update {
			out, _ := json.MarshalIndent(got, "", "  ")
			if err := os.WriteFile(golden, append(out, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		wantRaw, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{}
		if err := json.Unmarshal(wantRaw, &want); err != nil {
			t.Fatal(err)
		}
		var diffs []string
		for k, v := range want {
			if _, now := got[k]; !now && v == "{}" {
				// An object that was empty: its zero values are written
				// now, and checked as new keys below.
				continue
			}
			if got[k] != v {
				diffs = append(diffs, k+": was "+v+", now "+got[k])
			}
		}
		for k, v := range got {
			// A key the golden lacks was a zero value left out then.
			if _, ok := want[k]; !ok && v != "false" && v != `""` && v != "0" && v != "null" {
				diffs = append(diffs, k+": new, "+v)
			}
		}
		sort.Strings(diffs)
		if len(diffs) > 0 {
			t.Errorf("%s loads differently:\n  %s", filepath.Base(sample), strings.Join(diffs, "\n  "))
		}
	}
}

// emptied is openai-high with the values a user can clear: no effort (send
// none), no fallback, no reply ceiling, a dedicated summarizer on the main
// model, no persona, and session archives turned off.
func emptied() Config {
	cfg := Defaults()
	Presets["openai-high"](&cfg)
	cfg.Preset = ""
	cfg.Model.ReasoningEffort = ""
	cfg.Model.Fallback = nil
	cfg.Model.MaxTokens = 0
	cfg.Summarizer = Summarizer{Mode: SummarizerDedicated}
	cfg.Finalechat.Artifacts = false
	cfg.Persona = ""
	return cfg
}

// sameValues compares what a configuration runs with, leaving out the
// fields a writer is not asked to keep (its name and where it came from).
func sameValues(t *testing.T, how string, got, want Config) {
	t.Helper()
	strip := func(c Config) map[string]string {
		c.Name, c.Description, c.Preset, c.DefaultConfig, c.Instructions, c.Migrated = "", "", "", "", "", nil
		c.Relay.Profiles = nil
		return flatten(c)
	}
	g, w := strip(got), strip(want)
	var diffs []string
	for k, v := range w {
		if g[k] != v {
			diffs = append(diffs, k+": want "+v+", got "+g[k])
		}
	}
	for k, v := range g {
		if _, ok := w[k]; !ok {
			diffs = append(diffs, k+": want nothing, got "+v)
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		t.Errorf("%s does not reload to what it was given:\n  %s", how, strings.Join(diffs, "\n  "))
	}
}

// Every way eagent writes a configuration gives a file that loads back to
// the configuration it was given, cleared values included: the editor's
// save (Overlay), "write every value" (Pin), a saved preset (BundleBytes),
// and `eagent config --write` (Write).
func TestEveryWriterReloadsToTheConfigItWasGiven(t *testing.T) {
	noEagentEnv(t)
	cfg := emptied()

	for _, mode := range []string{"overlay", "pin"} {
		project := t.TempDir()
		writeConfig(t, project, `{"preset":"openai-high","persona":"terse","finalechat":{"artifacts":true}}`)
		var edits map[string]json.RawMessage
		if mode == "pin" {
			edits = Pin(cfg, "openai-high")
		} else {
			var err error
			if edits, err = Overlay(cfg, "openai-high"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := SaveFile(project, edits, "", false); err != nil {
			t.Fatal(err)
		}
		got, err := Load(project, "")
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		sameValues(t, mode, got, cfg)
	}

	// A saved preset loads over the project file, which here names a preset
	// with a fallback, a persona and archives on.
	project := t.TempDir()
	writeConfig(t, project, `{"preset":"openai-high","persona":"terse","finalechat":{"artifacts":true}}`)
	if _, err := SaveBundle(project, "mine", "", cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBundle(project, "", "mine")
	if err != nil {
		t.Fatal(err)
	}
	sameValues(t, "a saved preset", got, cfg)

	// config --write keeps the preset it was loaded with.
	project = t.TempDir()
	withPreset := cfg
	withPreset.Preset = "openai-high"
	if _, err := Write(project, withPreset); err != nil {
		t.Fatal(err)
	}
	if got, err = Load(project, ""); err != nil {
		t.Fatal(err)
	}
	sameValues(t, "config --write", got, cfg)
}

// Archives turned off stay off through every later save, whichever path
// makes it; a file that never mentions them still means on.
func TestArchivesTurnedOffStayOffThroughEverySave(t *testing.T) {
	noEagentEnv(t)
	project := t.TempDir()
	// What `eagent artifact disable` writes.
	writeConfig(t, project, `{"finalechat":{"artifacts":false}}`)
	for i, how := range []string{"editor save", "a second editor save", "write every value", "a request built from the loaded config"} {
		res := Resolve(project, "", "")
		if res.LoadError != "" {
			t.Fatal(res.LoadError)
		}
		cfg := res.Effective
		if cfg.Finalechat.Artifacts {
			t.Fatalf("before %s (step %d): archives are on", how, i)
		}
		var edits map[string]json.RawMessage
		var err error
		switch how {
		case "write every value":
			edits = Pin(cfg, res.File.Preset)
		case "a request built from the loaded config":
			// `eagent servers use` and the web UI send the configuration as
			// JSON; the settings service decodes it into an empty Config.
			raw, _ := json.Marshal(cfg)
			var decoded Config
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			edits, err = Overlay(decoded, res.File.Preset)
		default:
			edits, err = Overlay(cfg, res.File.Preset)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := SaveFile(project, edits, "", false); err != nil {
			t.Fatal(err)
		}
		if got, _ := Load(project, ""); got.Finalechat.Artifacts {
			t.Fatalf("%s turned archives back on; the file is now:\n%s", how, Resolve(project, "", "").File.Raw)
		}
	}
	// A saved preset made from it keeps them off.
	cfg, _ := Load(project, "")
	if _, err := SaveBundle(project, "quiet", "", cfg); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, project, `{}`)
	if got, err := LoadBundle(project, "", "quiet"); err != nil || got.Finalechat.Artifacts {
		t.Fatalf("saved preset: archives on=%v, %v", got.Finalechat.Artifacts, err)
	}
	// No key, and no file at all, still mean on.
	if got, _ := Load(project, ""); !got.Finalechat.Artifacts {
		t.Fatal("a file without the key turned archives off")
	}
	if got, _ := Load(t.TempDir(), ""); !got.Finalechat.Artifacts {
		t.Fatal("no file turned archives off")
	}
}

// EAGENT_ROLLOVER_TOKENS=0 means what rollover_tokens: 0 means: follow the
// model in use. It overrides a number the file fixes.
func TestRolloverTokensZeroFromTheEnvironmentFollowsTheModel(t *testing.T) {
	noEagentEnv(t)
	project := t.TempDir()
	writeConfig(t, project, `{"rollover_tokens":300000}`)
	t.Setenv("EAGENT_ROLLOVER_TOKENS", "0")
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RolloverTokens != 0 || cfg.Rollover() != cfg.Model.ContextTokens/2 {
		t.Fatalf("rollover_tokens = %d, rollover at %d", cfg.RolloverTokens, cfg.Rollover())
	}
	if src := Resolve(project, "", "").Sources["/rollover_tokens"]; src != "env:EAGENT_ROLLOVER_TOKENS" {
		t.Fatalf("source = %q", src)
	}
}

// A value the environment removes (here the window of a model no list
// knows) is credited to the variable, not to the layer that set it before.
func TestResolveCreditsTheEnvironmentForAValueItRemoved(t *testing.T) {
	noEagentEnv(t)
	project := t.TempDir()
	t.Setenv("EAGENT_MODEL", "nobody-knows-this-model")
	res := Resolve(project, "anthropic-high", "")
	if res.Effective.Model.ContextTokens != 0 {
		t.Fatalf("window = %d", res.Effective.Model.ContextTokens)
	}
	if src := res.Sources["/model/context_tokens"]; src != "env:EAGENT_MODEL" {
		t.Fatalf("source of the window = %q", src)
	}
}
