package settings

import (
	"strings"
	"testing"
)

// E5 (not changed): under EAGENT_SUMMARIZER_MODE and
// EAGENT_SUMMARIZER_MODEL, the presets the project can choose say the
// summaries run on the variable's model, as they would.
func TestK1APresetsSayTheSummarizerTheVariablesGive(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_SUMMARIZER_MODE", "dedicated")
	t.Setenv("EAGENT_SUMMARIZER_MODEL", "zai-org/GLM-5.3-Flash")
	s := &Service{Project: t.TempDir()}
	view, err := s.RemoteSnapshot(s.Grant())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range view.Descriptor.Suggestions["presets"] {
		if x.Value == "anthropic-high" {
			found = true
			if !strings.Contains(x.Detail, "summaries on zai-org/GLM-5.3-Flash") {
				t.Fatalf("anthropic-high: %q", x.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("no anthropic-high in %+v", view.Descriptor.Suggestions["presets"])
	}
}
