package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A project that clears the window of its model (context_tokens null, as the
// editor writes an emptied field) runs, by the runtime's rule
// (harness Runtime.window), on the window the catalog gives that model
// (config.KnownContext). doctor's rollover line reads cfg.Model.ContextTokens
// alone and takes 200,000 for the 0 it finds, so it prints half of 200,000
// and calls it "half of the model's context window".
func TestSR10DoctorRolloverLineOnNullWindowUsesTheModelsWindow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EAGENT_FINALECHAT", "")
	for _, preset := range []string{"anthropic-high", "openai-high", "opencode-high"} {
		project := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.File(project), []byte(`{"preset":"`+preset+`","model":{"context_tokens":null},"finalechat":{"enabled":false}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(project, "")
		if err != nil {
			t.Fatal(err)
		}
		known := config.KnownContext(project, cfg.Model.BaseURL, cfg.Model.Model)
		t.Logf("%s: model %s window in cfg %d, known %d", preset, cfg.Model.Model, cfg.Model.ContextTokens, known)
		if cfg.Model.ContextTokens != 0 || known <= 0 || known == 200_000 {
			continue
		}
		runtime := config.AutoRolloverWithin(known, cfg.Model.MaxTokens, 0)
		out := captureStdout(t, func() { cmdDoctor(project, "", "", false) })
		m := regexp.MustCompile(`conversation restarts at: (\d+) prompt tokens, (.*)`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no rollover line:\n%s", out)
		}
		n, _ := strconv.Atoi(m[1])
		if n != runtime {
			t.Fatalf("%s with a null window over its own model %s (the catalog's window %d): the session restarts at %d, doctor says: conversation restarts at: %d prompt tokens, %s", preset, cfg.Model.Model, known, runtime, n, m[2])
		}
		return
	}
	t.Skip("no preset model the catalog knows offline with a window other than 200,000")
}
