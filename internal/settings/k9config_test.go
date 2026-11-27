package settings

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
)

// EAGENT_CONFIG naming a built-in preset makes it the configuration in
// force (the loader takes a bundle name that is a built-in as that
// preset). The phone's "save as new preset" and a bundle saved with no
// source held it on main (LoadBundle); CurrentLayers looks for the bundle
// in force by its Name only, which a built-in leaves empty, and saves the
// built-in defaults' model instead.
func TestSR4SavesKeepABuiltInChosenAsTheBundle(t *testing.T) {
	relay.OfflineCatalogs()
	for _, path := range []string{"save_preset", "bundle with no source"} {
		t.Run(path, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", "")
			project := t.TempDir()
			writeProjectFile(t, project, `{"tick_seconds":45}`)
			t.Setenv("EAGENT_CONFIG", "anthropic-med")
			inForce, err := config.LoadBundle(project, "", "")
			if err != nil || inForce.Model.Model != "claude-opus-5-5" {
				t.Fatalf("setup: in force %q, %v", inForce.Model.Model, err)
			}
			s := &Service{Project: project}
			var got string
			if path == "save_preset" {
				if err := tryApply(t, s, control.Edit{Op: "set", Key: "/save_preset", Value: "saved"}); err != nil {
					t.Fatal(err)
				}
				saved, err := config.LoadBundle(project, "", "saved")
				if err != nil {
					t.Fatal(err)
				}
				got = saved.Model.Model
			} else {
				cfg, err := s.BundleConfig(BundleRequest{Name: "saved"})
				if err != nil {
					t.Fatal(err)
				}
				got = cfg.Model.Model
			}
			if got != inForce.Model.Model {
				t.Fatalf("saved preset runs %q; the configuration in force runs %q", got, inForce.Model.Model)
			}
		})
	}
}
