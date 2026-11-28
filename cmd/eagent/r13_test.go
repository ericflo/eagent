package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// `eagent presets use <built-in>` keeps the file's effort only as a level
// the preset's model offers: xhigh, written for openai-high, becomes the
// nearest GLM-5.3 or DeepSeek V4.1 Flash offers, as picking that preset in
// a session makes it (config.OfferedEffort), and the command says so. An
// effort the model offers stays as it was, and says nothing.
func TestPresetsUseKeepsOnlyAnEffortThePresetsModelOffers(t *testing.T) {
	for _, tc := range []struct{ effort, preset, want string }{
		{"xhigh", "glm", "high"},
		{"xhigh", "deepseek", "high"},
		{"medium", "glm", "medium"},
	} {
		t.Run(tc.effort+"-"+tc.preset, func(t *testing.T) {
			r3NoEnv(t)
			project := t.TempDir()
			r3WriteConfig(t, project, `{"preset":"openai-high","model":{"reasoning_effort":"`+tc.effort+`","max_tokens":20000},"tick_seconds":45}`)
			var code int
			out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", tc.preset}) })
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out)
			}
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			m, _ := config.Lookup(got.Model.BaseURL, got.Model.Model)
			if !slices.Contains(m.Efforts, got.Model.ReasoningEffort) {
				t.Fatalf("presets use %s left %s with unsupported effort %q (offered: %v)", tc.preset, got.Model.Model, got.Model.ReasoningEffort, m.Efforts)
			}
			if got.Model.ReasoningEffort != tc.want || got.Model.MaxTokens != 20000 || got.TickSeconds != 45 {
				t.Fatalf("after presets use %s: effort %q (want %q), max_tokens %d, tick %d", tc.preset, got.Model.ReasoningEffort, tc.want, got.Model.MaxTokens, got.TickSeconds)
			}
			if said := strings.Contains(out, tc.effort+" to "+tc.want); said != (tc.effort != tc.want) {
				t.Fatalf("what presets use said about the effort is wrong:\n%s", out)
			}
		})
	}
}
