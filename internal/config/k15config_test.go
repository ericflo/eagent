package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The converse of TestSR9KeptByReasonsAreOnesTheLoaderFollows: every saved
// preset the loader lays is kept, and the reason named is a layer that
// names it (flag, EAGENT_PRESET, EAGENT_CONFIG, default_config).
func TestSR10LoaderLaysOnlyWhatKeptByKeepsWithATrueReason(t *testing.T) {
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	for _, n := range []string{"fa", "fb", "vp", "vc", "dc"} {
		if _, err := SaveBundle(project, n, "", Defaults()); err != nil {
			t.Fatal(err)
		}
	}
	// A hand-written file under a built-in's name.
	if err := os.WriteFile(BundlePath(project, "glm"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := func(saved string) []string { return []string{"", "openai-high", "glm", saved, "missing"} }
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
						if res.Loads.Name != cfg.Name || res.Loads.From != cfg.NameFrom {
							bad++
							t.Errorf("flags %q/%q env %q/%q dc %q: Resolve says %v, loader %q from %q", fp, fb, vp, vc, dc, res.Loads, cfg.Name, cfg.NameFrom)
						}
						if cfg.Name != "" {
							why := strings.Join(KeptBy(res, fp, fb, cfg.Name), "; ")
							ok := false
							switch cfg.NameFrom {
							case "flag":
								ok = (fb == cfg.Name || fp == cfg.Name) && strings.Contains(why, "--config or --preset")
							case "EAGENT_PRESET":
								ok = vp == cfg.Name && strings.Contains(why, "EAGENT_PRESET")
							case "EAGENT_CONFIG":
								ok = vc == cfg.Name && strings.Contains(why, "EAGENT_CONFIG")
							case "default_config":
								ok = dc == cfg.Name && strings.Contains(why, "default_config")
							}
							if !ok {
								bad++
								t.Errorf("flags %q/%q env %q/%q dc %q: loader lays %q from %q; KeptBy says %q", fp, fb, vp, vc, dc, cfg.Name, cfg.NameFrom, why)
							}
						}
						if bad > 10 {
							t.FailNow()
						}
					}
				}
			}
		}
	}
}
