package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The converse of TestSR8GuardAndResolveMatchTheLoaderEverywhere: every
// reason KeptBy gives besides default_config (a flag, EAGENT_PRESET,
// EAGENT_CONFIG) says that reason lays the preset, so the loader under
// the same flags and variables must lay it. A refusal that names a reason
// the loader does not follow tells the user something false, and keeps a
// preset no session starts on.
func TestSR9KeptByReasonsAreOnesTheLoaderFollows(t *testing.T) {
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
						for _, name := range []string{"fa", "fb", "vp", "vc", "dc"} {
							for _, why := range KeptBy(res, fp, fb, name) {
								if strings.Contains(why, "default_config") {
									continue
								}
								if cfg.Name != name {
									bad++
									t.Errorf("flags preset=%q config=%q, EAGENT_PRESET=%q EAGENT_CONFIG=%q default_config=%q: %q is refused as in force (%q), but the loader lays %q", fp, fb, vp, vc, dc, name, why, cfg.Name)
								}
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
