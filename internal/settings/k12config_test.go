package settings

import (
	"context"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A server started with --config other lays other, whatever EAGENT_PRESET
// or EAGENT_CONFIG names: the guard follows what the loader lays, so the
// saved preset a variable names, outranked, can be deleted, and other
// cannot.
func TestK12ADeleteGuardFollowsTheBundleTheLoaderLays(t *testing.T) {
	for _, variable := range []string{"EAGENT_PRESET", "EAGENT_CONFIG"} {
		t.Run(variable, func(t *testing.T) {
			t.Setenv("EAGENT_PRESET", "")
			t.Setenv("EAGENT_CONFIG", "")
			t.Setenv("EAGENT_FINALECHAT", "")
			project := t.TempDir()
			for _, name := range []string{"work", "other"} {
				if _, err := config.SaveBundle(project, name, "", config.Defaults()); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(variable, "work")
			if cfg, err := config.LoadBundle(project, "", "other"); err != nil || cfg.Name != "other" {
				t.Fatalf("setup: --config other lays %q (%v)", cfg.Name, err)
			}
			s := &Service{Project: project, Bundle: "other"}
			if err := s.DeleteBundle(context.Background(), "work"); err != nil {
				t.Fatalf("deleting work, which %s names but --config other outranks: %v", variable, err)
			}
			if err := s.DeleteBundle(context.Background(), "other"); err == nil {
				t.Fatal("deleted other, the bundle the server lays")
			}
		})
	}
}
