package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// TestK10AnEditUnderADefaultBundleWritesOnlyItsField with a "name" in the
// file, as `eagent --config work config --write` writes one (main's did
// too): CurrentLayers takes the file's name for a bundle Resolve already
// laid and leaves the default bundle out, so the picks compare the
// loader's candidate (bundle other, preset glm) with the file's layers
// (openai-high) again, and an edit of tick_seconds rewrites the file's
// effort, the fault ff9bba2 fixed.
func TestSR6AnEditUnderADefaultBundleWritesOnlyItsFieldWithANamedFile(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	writeProjectFile(t, project, `{"name":"work","preset":"openai-high","default_config":"other","model":{"reasoning_effort":"xhigh"}}`)
	if err := os.MkdirAll(filepath.Dir(config.BundlePath(project, "other")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "other"), []byte(`{"preset":"glm"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err := config.LoadBundle(project, "", ""); err != nil || cfg.Name != "other" || cfg.Preset != "glm" {
		t.Fatalf("setup: sessions run %q/%q (%v)", cfg.Name, cfg.Preset, err)
	}
	if cur, err := CurrentLayers(project, "", ""); err != nil || cur.Preset != "glm" {
		t.Errorf("CurrentLayers: preset %q (%v), sessions run glm (bundle other)", cur.Preset, err)
	}
	if err := tryApply(t, &Service{Project: project}, control.Edit{Op: "set", Key: "/tick_seconds", Value: float64(30)}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(config.File(project))
	values, _ := fileValues(string(raw))
	if v, _ := pointerValue(values, "/model/reasoning_effort"); v != "xhigh" {
		t.Fatalf("an edit of tick_seconds changed the file's effort to %v:\n%s", v, raw)
	}
}
