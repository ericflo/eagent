package settings

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// clearRouteEnv removes every variable that sets a route field, a preset or
// a bundle for the test, so it does not depend on the shell running it.
func clearRouteEnv(t *testing.T) {
	t.Helper()
	names := []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE"}
	for _, suffix := range []string{"MODEL", "PROTOCOL", "BASE_URL", "API_KEY_ENV", "REASONING_EFFORT", "MAX_TOKENS", "REPLAY_REASONING"} {
		names = append(names, "EAGENT_"+suffix, "EAGENT_ORCHESTRATOR_"+suffix, "EAGENT_SUMMARIZER_"+suffix)
	}
	for _, n := range names {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
}

// A summarizer route the environment makes is not written in part: a save
// of anything else while EAGENT_SUMMARIZER_MODEL is set leaves config.json
// loadable once the variable is gone.
func TestSaveKeepsEnvSummarizerRouteOutOfTheFile(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_SUMMARIZER_MODEL", "zai-org/GLM-5.3-Flash")
	project := t.TempDir()
	res := config.Resolve(project, "", "")
	if res.LoadError != "" || res.Effective.Summarizer.Model == nil {
		t.Fatalf("resolve: %q %+v", res.LoadError, res.Effective.Summarizer)
	}
	cfg := res.Effective
	cfg.TickSeconds = 45
	raw, _ := json.Marshal(cfg)
	out, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(config.File(project))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(written, &file); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file["summarizer"]), "model") {
		t.Fatalf("a summarizer route was written: %s", written)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Pointer != "/summarizer/model" || out.Skipped[0].Env != "EAGENT_SUMMARIZER_MODEL" {
		t.Fatalf("skipped = %+v", out.Skipped)
	}
	os.Unsetenv("EAGENT_SUMMARIZER_MODEL")
	loaded, err := config.Load(project, "")
	if err != nil {
		t.Fatalf("load after the variable is gone: %v\n%s", err, written)
	}
	if loaded.TickSeconds != 45 {
		t.Fatalf("tick_seconds = %d", loaded.TickSeconds)
	}
}

// A summarizer route written in the file keeps its own fields: only the
// field the variable overrides is left as the file has it.
func TestSaveWithWrittenSummarizerRouteKeepsItsFields(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	if err := os.MkdirAll(project+"/.agents/eagent", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EAGENT_SUMMARIZER_MODEL", "zai-org/GLM-5.3-Flash")
	res := config.Resolve(project, "", "")
	cfg := res.Effective
	cfg.Summarizer.Model.ReasoningEffort = "low"
	raw, _ := json.Marshal(cfg)
	if _, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw}); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("EAGENT_SUMMARIZER_MODEL")
	loaded, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if m := loaded.Summarizer.Model; m == nil || m.Model != "zai-org/GLM-5.3" || m.ReasoningEffort != "low" {
		t.Fatalf("summarizer = %+v", loaded.Summarizer.Model)
	}
}
