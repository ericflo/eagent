package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
)

// Under a saved preset in force through default_config, which names a
// preset of its own, a remote edit of something that is no route writes
// that field alone: the route picks do not take the bundle's preset for a
// change of the file's.
func TestK10AnEditUnderADefaultBundleWritesOnlyItsField(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	project := t.TempDir()
	writeProjectFile(t, project, `{"preset":"openai-high","default_config":"work","model":{"reasoning_effort":"xhigh"}}`)
	if err := os.MkdirAll(filepath.Dir(config.BundlePath(project, "work")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "work"), []byte(`{"preset":"glm"}`), 0o644); err != nil {
		t.Fatal(err)
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
