package main

import (
	"strings"
	"testing"
)

// Under EAGENT_PRESET=work, a saved preset, `eagent presets list` lists
// every saved preset as it loads (none is "invalid") and marks work, the
// one sessions run.
func TestK12PresetsListUnderEagentPresetShowsEverySavedPreset(t *testing.T) {
	project := sr6TwoPresets(t)
	t.Setenv("EAGENT_PRESET", "work")
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"list"}) })
	if code != 0 || strings.Contains(out, "invalid") || !strings.Contains(out, "* work") {
		t.Fatalf("presets list under EAGENT_PRESET=work: exit %d\n%s", code, out)
	}
}
