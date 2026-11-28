package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// Finding 19: under a project file with a dedicated summarizer, a running
// session that keeps its summaries inline (no summarizer route) is offered
// each configuration without a summarizer route implied or "summaries on"
// said, and choosing one with what it implies is accepted. A session with
// a dedicated summarizer keeps both.
func TestR10ConfigSuggestionsFollowTheSessionsSummarizer(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_PROTOCOL", "EAGENT_REASONING_EFFORT", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL"} {
		t.Setenv(n, "")
	}
	for _, n := range []string{"OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "TOGETHER_API_KEY"} {
		t.Setenv(n, "test")
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.File(project)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.File(project), []byte(`{"summarizer":{"mode":"dedicated"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dedicated, err := config.Load(project, "")
	if err != nil || dedicated.Summarizer.Mode != config.SummarizerDedicated {
		t.Fatalf("setup: %v (%s)", err, dedicated.Summarizer.Mode)
	}
	for _, c := range []struct {
		name       string
		values     runtimecontrol.Values
		summarizer bool
	}{
		{"inline session", runtimecontrol.FromConfigFor(project, config.Defaults()), false},
		{"dedicated session", runtimecontrol.FromConfigFor(project, dedicated), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			view, err := runtimecontrol.View(project, "session", "gen", c.values, true)
			if err != nil {
				t.Fatal(err)
			}
			grant, _ := runtimecontrol.Grant(project, "session")
			decorateSessionView(project, &view, grant)
			var chosen *control.Suggestion
			for _, s := range view.Descriptor.Suggestions["configs"] {
				if s.Value == "anthropic-high" {
					chosen = &s
				}
			}
			if chosen == nil {
				t.Fatal("no anthropic-high suggestion")
			}
			edits := []control.Edit{{Op: "set", Key: "/config", Value: "anthropic-high"}}
			for k, v := range chosen.Implies {
				edits = append(edits, control.Edit{Op: "set", Key: k, Value: v})
			}
			p := control.Proposal{Operation: "settings.apply", SchemaVersion: runtimecontrol.Schema, ExpectedVersion: view.Snapshot.Version, Generation: "gen", Edits: edits}
			if _, err := runtimecontrol.ApplyValues(project, c.values, p, view, grant); err != nil {
				t.Fatalf("/config = anthropic-high with what it implies refused: %v", err)
			}
			_, implied := chosen.Implies["/routes/summarizer/model"]
			said := strings.Contains(chosen.Detail, "summaries on")
			if implied != c.summarizer || said != c.summarizer {
				t.Fatalf("summarizer route implied %v, detail %q; the session has one: %v", implied, chosen.Detail, c.summarizer)
			}
		})
	}
}
