package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Every combination of flag, variable and default_config, saved or
// built-in: the bundle the loader lays is one the guard keeps, and the
// configuration Resolve shows lays the same bundle and preset.
func TestSR8GuardAndResolveMatchTheLoaderEverywhere(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	for _, n := range []string{"fa", "fb", "vp", "vc", "dc"} {
		if _, err := SaveBundle(project, n, "", Defaults()); err != nil {
			t.Fatal(err)
		}
	}
	opts := func(saved string) []string { return []string{"", "openai-high", saved} }
	bad := 0
	for _, dc := range opts("dc") {
		raw := "{}"
		if dc != "" {
			raw = fmt.Sprintf(`{"default_config":%q}`, dc)
		}
		if err := os.MkdirAll(filepath.Dir(File(project)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(File(project), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, vp := range opts("vp") {
			for _, vc := range opts("vc") {
				t.Setenv("EAGENT_PRESET", vp)
				t.Setenv("EAGENT_CONFIG", vc)
				for _, fp := range opts("fa") {
					for _, fb := range opts("fb") {
						cfg, err := LoadBundle(project, fp, fb)
						if err != nil {
							continue
						}
						res := Resolve(project, fp, fb)
						if cfg.Name != "" && !IsDefaultBundle(res, cfg.Name) {
							bad++
							t.Errorf("flags preset=%q config=%q, EAGENT_PRESET=%q EAGENT_CONFIG=%q default_config=%q: the loader lays %q, which the guard lets go", fp, fb, vp, vc, dc, cfg.Name)
						}
						// Resolve leaves the bundle loading takes by default
						// (EAGENT_CONFIG, default_config) to its callers.
						if vc == "" && dc == "" && res.LoadError == "" && res.Effective.Preset != cfg.Preset {
							bad++
							t.Errorf("flags preset=%q config=%q, EAGENT_PRESET=%q EAGENT_CONFIG=%q default_config=%q: Resolve lays preset %q, the loader %q", fp, fb, vp, vc, dc, res.Effective.Preset, cfg.Preset)
						}
						if bad > 20 {
							t.FailNow()
						}
					}
				}
			}
		}
	}
}
