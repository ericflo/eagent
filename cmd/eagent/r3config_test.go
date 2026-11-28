package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/prompts"
)

// The help's list of built-in presets is the list of presets: every one is
// there, and no name that now stands for another is offered as one.
func TestTheHelpListsTheBuiltInPresets(t *testing.T) {
	start := strings.Index(usage, "built-ins:")
	end := strings.Index(usage, "--config <n>")
	if start < 0 || end < start {
		t.Fatal("no preset list in the help")
	}
	var listed []string
	for _, word := range regexp.MustCompile(`[a-z0-9.|-]+`).FindAllString(usage[start+len("built-ins:"):end], -1) {
		if word == "default" || word == "or" || word == "a" || word == "provider" || word == "tier" {
			continue
		}
		base, tiers, isTier := strings.Cut(word, "|")
		if !isTier {
			listed = append(listed, word)
			continue
		}
		family, _, _ := strings.Cut(base, "-")
		listed = append(listed, base)
		for _, tier := range strings.Split(tiers, "|") {
			listed = append(listed, family+"-"+tier)
		}
	}
	want := config.PresetNames()
	sort.Strings(listed)
	sort.Strings(want)
	if !slices.Equal(listed, want) {
		t.Fatalf("the help lists %v\nthe presets are %v", listed, want)
	}
}

// captureStdout runs fn and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

func r3NoEnv(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_PROTOCOL", "EAGENT_API_KEY_ENV", "EAGENT_REASONING_EFFORT", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL", "EAGENT_ROLLOVER_TOKENS"} {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
	t.Setenv("EAGENT_FINALECHAT", "off")
}

func r3WriteConfig(t *testing.T, project, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// `eagent presets use <built-in>` makes the project run that preset's
// model: the model the file named, and where it was reached, give way; the
// file's effort and other settings stay.
func TestPresetsUseGivesThePresetsModel(t *testing.T) {
	r3NoEnv(t)
	project := t.TempDir()
	r3WriteConfig(t, project, `{"model":{"protocol":"openai-chat","base_url":"https://openrouter.ai/api/v1","model":"z-ai/glm-5.3","api_key_env":"OPENROUTER_API_KEY","reasoning_effort":"medium"},"tick_seconds":45}`)
	var code int
	out := captureStdout(t, func() { code = cmdConfig(project, "", "", false, []string{"use", "anthropic-high"}) })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	want, _ := config.Load(t.TempDir(), "anthropic-high")
	got, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model.Model != want.Model.Model || got.Model.BaseURL != want.Model.BaseURL || got.Model.Protocol != want.Model.Protocol || got.Model.APIKeyEnv != want.Model.APIKeyEnv {
		t.Fatalf("after presets use anthropic-high the model is %+v", got.Model)
	}
	if got.Model.ReasoningEffort != "medium" || got.TickSeconds != 45 {
		t.Fatalf("the file's own settings went: effort %q, tick %d", got.Model.ReasoningEffort, got.TickSeconds)
	}
	if !strings.Contains(out, "z-ai/glm-5.3") {
		t.Fatalf("nothing said about the model the file named:\n%s", out)
	}
}

// `eagent servers use` writes nothing when the environment pins a field of
// the route it would write: half a route in the file is a broken one.
func TestServersUseRefusesARouteTheEnvironmentPins(t *testing.T) {
	r3NoEnv(t)
	project := t.TempDir()
	r3WriteConfig(t, project, `{"tick_seconds":45}`)
	t.Setenv("EAGENT_MODEL", "pinned-model")
	t.Setenv("OPENROUTER_API_KEY", "test")
	var code int
	captureStdout(t, func() { code = cmdServers(project, []string{"use", "openrouter", "z-ai/glm-5.3"}, serverCLIOpts{}) })
	if code == 0 {
		t.Fatal("servers use reported success under EAGENT_MODEL")
	}
	if raw, _ := os.ReadFile(config.File(project)); string(raw) != `{"tick_seconds":45}` {
		t.Fatalf("the file changed:\n%s", raw)
	}
}

// `eagent doctor` reports a prompt template that does not parse, an
// override of a prompt nothing reads any more, and the configuration's own
// warnings.
func TestDoctorReportsPromptsAndConfigProblems(t *testing.T) {
	r3NoEnv(t)
	project := t.TempDir()
	r3WriteConfig(t, project, `{"rollover_tokens":190000}`)
	dir := prompts.Dir(project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "ORCHESTRATOR.md"), []byte("Be brief."), 0o644)
	os.WriteFile(filepath.Join(dir, "USER_MESSAGE.md"), []byte("{{ .Text "), 0o644)
	var code int
	out := captureStdout(t, func() { code = cmdDoctor(project, "", "", false) })
	if code == 0 {
		t.Fatalf("a broken prompt template passed:\n%s", out)
	}
	for _, want := range []string{"✗ USER_MESSAGE.md", "ORCHESTRATOR.md", "no longer read", "rollover_tokens (190000) is within 10%"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor does not say %q:\n%s", want, out)
		}
	}
	// With the template repaired the prompts pass and say where they come from.
	os.WriteFile(filepath.Join(dir, "USER_MESSAGE.md"), []byte("{{ .Text }}"), 0o644)
	out = captureStdout(t, func() { cmdDoctor(project, "", "", false) })
	if strings.Contains(out, "✗ USER_MESSAGE.md") || !strings.Contains(out, "USER_MESSAGE.md") {
		t.Errorf("after the repair:\n%s", out)
	}
}
