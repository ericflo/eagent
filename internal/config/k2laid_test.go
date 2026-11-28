package config

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// LaidPreset is loading's own choice (chooseLayers), so the preset it
// gives is the one loading lays. What it says named that preset is checked
// here against the settings themselves, over every combination of the two
// variables, the file's default_config and the file's own preset, each
// naming nothing, a built-in preset, or a saved preset (with a preset of
// its own, or none).
func TestK2LaidPresetNamesTheLayerThatLaidIt(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(BundlesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := map[string]string{"withpreset": `{"preset":"openrouter-high"}`, "plain": `{"persona":"terse"}`}
	for name, body := range saved {
		if err := os.WriteFile(BundlePath(project, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// What a name lays as a preset: itself when built-in, its own preset
	// key when a saved preset.
	builtIn := func(name string) string {
		canon, ok := ResolvePreset(name)
		if !ok {
			return ""
		}
		return canon
	}
	choices := []string{"", "glm", "openai-high", "astra", "openrouter-high", "withpreset", "plain"}
	loaded, named := 0, map[string]int{}
	for _, envPreset := range choices {
		for _, envConfig := range choices {
			for _, def := range choices {
				for _, own := range []string{"", "anthropic-high"} {
					t.Setenv("EAGENT_PRESET", envPreset)
					t.Setenv("EAGENT_CONFIG", envConfig)
					file := map[string]json.RawMessage{}
					if def != "" {
						file["default_config"], _ = json.Marshal(def)
					}
					if own != "" {
						file["preset"], _ = json.Marshal(own)
					}
					at := fmt.Sprintf("EAGENT_PRESET=%q EAGENT_CONFIG=%q default_config=%q preset=%q", envPreset, envConfig, def, own)
					laid, from, err := LaidPreset(project, file)
					cfg, lerr := LoadBundleWithFile(project, "", "", file)
					if err != nil {
						if lerr == nil {
							t.Errorf("%s: LaidPreset: %v; loading has no error", at, err)
						}
						continue
					}
					named[from]++
					if lerr == nil {
						loaded++
						// Nothing here names a model, so the model that
						// loads is the laid preset's.
						want := Defaults()
						if laid != "" {
							Presets[laid](&want)
						}
						if cfg.Model.Model != want.Model.Model {
							t.Errorf("%s: LaidPreset says %q; loading runs %s", at, laid, cfg.Model.Model)
						}
					}
					ok := false
					switch from {
					case "":
						ok = laid == builtIn(own)
					case "EAGENT_PRESET":
						ok = laid != "" && laid == builtIn(envPreset)
					case "EAGENT_CONFIG":
						ok = laid != "" && laid == builtIn(envConfig)
					case "default_config":
						ok = laid != "" && laid == builtIn(def) && envConfig == ""
					case "bundle":
						ok = laid == "openrouter-high" && lerr == nil && cfg.Name == "withpreset"
					}
					if !ok {
						t.Errorf("%s: LaidPreset says %q named by %q (loading: %q from %q, %v)", at, laid, from, cfg.Name, cfg.NameFrom, lerr)
					}
					// The layer it names is the one that does it: with
					// that layer gone, the answer is another. (Another
					// layer that happens to name the same preset is not
					// blamed.)
					without := map[string]json.RawMessage{}
					for k, v := range file {
						without[k] = v
					}
					switch from {
					case "EAGENT_PRESET", "EAGENT_CONFIG":
						t.Setenv(from, "")
					case "default_config":
						delete(without, "default_config")
					case "bundle":
						// (The layer that lays the saved preset is
						// loading's own word, NameFrom. Another layer may
						// name the same saved preset after it.)
						if cfg.NameFrom == "default_config" {
							delete(without, "default_config")
						} else {
							t.Setenv(cfg.NameFrom, "")
						}
						if after, _ := LoadBundleWithFile(project, "", "", without); after.Name == cfg.Name && after.NameFrom == cfg.NameFrom {
							t.Errorf("%s: the saved preset %q is laid by %q, and still is with that layer gone", at, cfg.Name, cfg.NameFrom)
						}
						continue
					default:
						continue
					}
					if laid2, from2, _ := LaidPreset(project, without); laid2 == laid && from2 == from {
						t.Errorf("%s: LaidPreset says %q named by %q, and says the same with that layer gone", at, laid, from)
					}
				}
			}
		}
	}
	for _, from := range []string{"", "EAGENT_PRESET", "EAGENT_CONFIG", "default_config", "bundle"} {
		if named[from] == 0 {
			t.Errorf("no combination was named by %q: %v", from, named)
		}
	}
	if loaded == 0 {
		t.Error("no combination loaded")
	}
}
