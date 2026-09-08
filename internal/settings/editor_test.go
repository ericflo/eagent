package settings

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/artifact"
)

func TestEditorDataUsesCapturedBundlesAndExcludesPrivateValues(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(config.BundlesDir(project), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"preset":"glm","task_concurrency":7,"unknown_private":"never-publish","instructions":"private-instructions"}`)
	if err := os.WriteFile(config.BundlePath(project, "fixture"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"persona":"outside-private"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, config.BundlePath(project, "outside")); err != nil {
		t.Fatal(err)
	}
	view, err := (&Service{Project: project}).EditorData()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"never-publish", "private-instructions", "outside-private"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("published %s", forbidden)
		}
	}
	bundle := view["bundles"].(map[string]any)["fixture"].(map[string]any)
	if bundle["sha256"] != artifact.Digest(raw) || bundle["config"].(config.Config).TaskConcurrency != 7 {
		t.Fatalf("bundle identity and values do not match captured bytes: hash=%v want=%v config=%+v", bundle["sha256"], artifact.Digest(raw), bundle["config"])
	}
	if len(view["presets"].(map[string]config.Config)) != len(config.PresetNames()) {
		t.Fatal("incomplete preset catalog")
	}
}
