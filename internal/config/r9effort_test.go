package config

import (
	"encoding/json"
	"testing"
)

func r9File(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Choosing a preset keeps the project file's effort only as a level the
// preset's model offers: a file written for openai-high at xhigh puts
// GLM-5.3, which offers low, medium and high, at high.
func TestR9ChosenPresetTakesAnEffortItsModelOffers(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_PROTOCOL", "EAGENT_REASONING_EFFORT", "EAGENT_ORCHESTRATOR_MODEL", "EAGENT_ORCHESTRATOR_REASONING_EFFORT"} {
		t.Setenv(n, "")
	}
	glm, ok := Lookup(togetherURL, "zai-org/GLM-5.3")
	if !ok || len(glm.Efforts) == 0 {
		t.Fatal("GLM-5.3 is not in the catalog with its efforts")
	}
	highest := glm.Efforts[0]
	for _, e := range glm.Efforts {
		if EffortIndex(e) > EffortIndex(highest) {
			highest = e
		}
	}
	if contains(glm.Efforts, "xhigh") || highest != "high" {
		t.Fatalf("GLM-5.3 offers %v", glm.Efforts)
	}
	files := map[string]string{
		"full route":  `{"preset":"openai-high","model":{"protocol":"openai-responses","base_url":"https://api.openai.com/v1","model":"gpt-6.2-astra","reasoning_effort":"xhigh"}}`,
		"effort only": `{"preset":"openai-high","model":{"reasoning_effort":"xhigh"}}`,
	}
	for name, raw := range files {
		cfg, err := ChosenPresetWithFile(t.TempDir(), "glm", r9File(t, raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cfg.Model.Model != "zai-org/GLM-5.3" || cfg.Model.ReasoningEffort != highest {
			t.Fatalf("%s: glm chosen gives %s at %q, want zai-org/GLM-5.3 at %q", name, cfg.Model.Model, cfg.Model.ReasoningEffort, highest)
		}
	}
	// An effort the preset's model offers stays, on either model.
	cfg, err := ChosenPresetWithFile(t.TempDir(), "glm", r9File(t, `{"preset":"openai-high","model":{"reasoning_effort":"low"}}`))
	if err != nil || cfg.Model.ReasoningEffort != "low" {
		t.Fatalf("glm over a file at low: %q, %v", cfg.Model.ReasoningEffort, err)
	}
	cfg, err = ChosenPresetWithFile(t.TempDir(), "openai-high", r9File(t, files["effort only"]))
	if err != nil || cfg.Model.ReasoningEffort != "xhigh" {
		t.Fatalf("openai-high over a file at xhigh: %q, %v", cfg.Model.ReasoningEffort, err)
	}
	// No effort (send none) is not a level the model lacks.
	cfg, err = ChosenPresetWithFile(t.TempDir(), "glm", r9File(t, `{"model":{"reasoning_effort":null}}`))
	if err != nil || cfg.Model.ReasoningEffort != "" {
		t.Fatalf("glm over a file with no effort: %q, %v", cfg.Model.ReasoningEffort, err)
	}
}
