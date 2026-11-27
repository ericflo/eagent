package harness

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/runtimecontrol"
)

// Choosing a configuration whose summaries are inline keeps the session's
// dedicated summarizer route (the /config handling keeps its pick): the
// chosen configuration's main route does not replace it, even when the
// summarizer runs the same model.
func TestK8AnInlineChosenConfigurationKeepsTheSummarizersRoute(t *testing.T) {
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_MODEL", "EAGENT_BASE_URL", "EAGENT_SUMMARIZER_MODE", "EAGENT_SUMMARIZER_MODEL"} {
		t.Setenv(n, "")
	}
	for _, n := range []string{"EAGENT_TEST_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(n, "x")
	}
	useFakeRelays(t, newFakeSeat("ok"))
	project := t.TempDir()
	chosen, err := config.ChosenPreset(project, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.Summarizer.Mode == config.SummarizerDedicated {
		t.Fatal("setup: openai-high's summaries are not inline")
	}
	cfg := testConfig("http://127.0.0.1:1")
	own := chosen.Model
	own.MaxTokens, own.Fallback = 2000, nil
	cfg.Summarizer = config.Summarizer{Mode: config.SummarizerDedicated, Model: &own}
	rt, err := New(cfg, Options{Project: project}, &fakeUI{input: make(chan string)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.sess.Close()
	before := rt.activeSettings
	view, err := runtimecontrol.View(project, "session", "gen", before, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := runtimecontrol.Grant(project, "session")
	p := control.Proposal{Operation: "settings.apply", SchemaVersion: runtimecontrol.Schema, ExpectedVersion: view.Snapshot.Version, Generation: "gen", Edits: []control.Edit{{Op: "set", Key: "/config", Value: "openai-high"}}}
	after, err := runtimecontrol.ApplyValues(project, before, p, view, grant)
	if err != nil {
		t.Fatal(err)
	}
	if after.Routes[runtimecontrol.RouteSummarizer] != before.Routes[runtimecontrol.RouteSummarizer] {
		t.Fatalf("setup: the summarizer's pick changed: %+v", after.Routes[runtimecontrol.RouteSummarizer])
	}
	swaps, err := rt.routeChanges(before.Routes, after.Routes, "openai-high")
	if err != nil {
		t.Fatal(err)
	}
	for _, sw := range swaps {
		if sw.who == runtimecontrol.RouteSummarizer {
			t.Fatalf("the summarizer's route was replaced by openai-high's main route: max_tokens %d fallback %v (its own: 2000, none)", sw.conf.MaxTokens, sw.conf.Fallback != nil)
		}
	}
}
