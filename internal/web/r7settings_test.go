package web

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Finding 29: the phone app's settings page follows config.Overlay's rules
// when it saves the project's settings: a preset's fallback cannot be
// removed by unsetting the file's overrides of it, and a new model keeps
// the window the form shows.
func TestArtifactSettingsSaveFollowsOverlay(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	for _, c := range []string{"fallback", "window"} {
		out, err := exec.Command("node", filepath.Join("testdata", "r7settings-artifact-settings.mjs"), c).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", c, err, out)
		}
	}
}
