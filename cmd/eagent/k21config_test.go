package main

import (
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A saved preset that EAGENT_CONFIG lays over the file sets only the
// route's protocol. `presets use glm` over a file naming its own model
// refuses, and its reason prints the route that would run and the
// preset's own in full, and names the part that differs: the protocol.
// (Printed as model, address and key alone, the route that "would run
// instead" read as the preset's own.)
func TestK21PresetsUseRefusalPrintsBothRoutesAndWhatDiffers(t *testing.T) {
	project := sr12Clean(t)
	file := `{"model":{"model":"zai-org/GLM-5.2"},"finalechat":{"enabled":false}}`
	if err := os.WriteFile(config.File(project), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "proto"), []byte(`{"model":{"protocol":"openai-responses"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_CONFIG", "proto")
	if cfg, err := config.Load(project, ""); err != nil || cfg.Name != "proto" {
		t.Fatalf("loads before: %q %v", cfg.Name, err)
	}
	want := config.Defaults()
	runs := want.Model
	runs.Protocol = "openai-responses"
	var code int
	said := k18Stderr(t, func() {
		captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "glm"}) })
	})
	if raw, _ := os.ReadFile(config.File(project)); code == 0 || string(raw) != file {
		t.Fatalf("not refused: exit %d, file %s", code, raw)
	}
	for _, part := range []string{routeText(runs) + " would", "the preset's (" + routeText(want.Model) + ")", "in its protocol;"} {
		if !strings.Contains(said, part) {
			t.Errorf("the refusal lacks %q: %q", part, said)
		}
	}
}

// A route with no key variable is said so, in the refusal as in the note
// after `servers use`.
func TestK21ARouteWithNoKeyVariableSaysSo(t *testing.T) {
	m := config.Defaults().Model
	m.APIKeyEnv = ""
	if got := routeText(m); !strings.HasSuffix(got, ", no key variable)") || strings.Contains(got, "$") {
		t.Fatalf("route text %q", got)
	}
	if got := routeParts(m, config.Defaults().Model); got != "key variable" {
		t.Fatalf("parts that differ %q; want the key variable", got)
	}
}
