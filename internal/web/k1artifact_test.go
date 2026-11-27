package web

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The phone app's settings page saves a route's window by the server's
// rules: a route the form moves to another address for the same model
// writes the window shown (finding 10, config.Overlay), and on a route the
// shell's variable moves, a window left as loaded is not written while
// one the user enters is (settings.SaveLocked).
func TestK1ArtifactSettingsSaveTheWindowAsTheServerDoes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	for _, c := range []string{"moved-address", "pinned-untouched", "pinned-entered"} {
		out, err := exec.Command("node", filepath.Join("testdata", "k1-artifact-settings.mjs"), c).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", c, err, out)
		}
	}
}
