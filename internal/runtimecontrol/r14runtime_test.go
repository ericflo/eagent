package runtimecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
	"github.com/ericflo/eagent/internal/settings"
)

// r14Env clears the variables that name a route or a bundle, and keeps
// the settings descriptor from starting the agents installed here to list
// their models.
func r14Env(t *testing.T) {
	t.Helper()
	relay.OfflineCatalogs()
	for _, suffix := range []string{"MODEL", "PROTOCOL", "BASE_URL", "API_KEY_ENV", "REASONING_EFFORT", "MAX_TOKENS", "REPLAY_REASONING"} {
		for _, prefix := range []string{"EAGENT_", "EAGENT_ORCHESTRATOR_", "EAGENT_SUMMARIZER_"} {
			t.Setenv(prefix+suffix, "")
			os.Unsetenv(prefix + suffix)
		}
	}
	for _, n := range []string{"EAGENT_PRESET", "EAGENT_CONFIG", "EAGENT_SUMMARIZER_MODE", "EAGENT_ROLLOVER_TOKENS"} {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
}

// r14LocalGLM registers a keyless server whose list says it serves the
// defaults' zai-org/GLM-5.3 with a 32k window, and returns its address.
func r14LocalGLM(t *testing.T, project string) string {
	t.Helper()
	res, err := config.RegisterServer(context.Background(), project, config.ProbeOptions{URL: "http://127.0.0.1:9/v1", ID: "local", Label: "Local"}, true)
	if err != nil {
		t.Fatal(err)
	}
	config.UpdateLiveCacheForServer(project, "local", []config.Model{{Provider: "local", ID: "zai-org/GLM-5.3", Context: 32_768}}, "")
	return res.Server.BaseURL
}

// A remote edit of /model/base_url alone, to a server that serves the same
// model with 32k, leaves the route with that server's window.
func TestARemoteAddressEditTakesTheServersWindow(t *testing.T) {
	r14Env(t)
	project := t.TempDir()
	url := r14LocalGLM(t, project)
	s := &settings.Service{Project: project}
	g := s.Grant()
	view, err := s.RemoteSnapshot(g)
	if err != nil {
		t.Fatal(err)
	}
	_, after, err := s.Apply(context.Background(), control.Proposal{
		Operation: "settings.apply", SchemaVersion: settings.SchemaVersion, ExpectedVersion: view.Snapshot.Version,
		Edits: []control.Edit{{Op: "set", Key: "/model/base_url", Value: url}},
	}, g)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(after.Snapshot.Effective["/model/context_tokens"]); got != "32768" {
		t.Fatalf("window after the edit = %s, want 32768", got)
	}
	cfg, err := config.Load(project, "")
	if err != nil || cfg.Model.ContextTokens != 32_768 || cfg.Rollover() != config.AutoRolloverWithin(32_768, cfg.Model.MaxTokens, 0) {
		t.Fatalf("loaded window %d rollover %d err %v", cfg.Model.ContextTokens, cfg.Rollover(), err)
	}
}

// Under EAGENT_BASE_URL, the window in force is that server's, so a save
// from the editor (which sends what is in force) does not write it to the
// file: there it would be the window of the file's own server once the
// variable is unset.
func TestASaveUnderAnAddressVariableDoesNotWriteItsWindow(t *testing.T) {
	r14Env(t)
	project := t.TempDir()
	url := r14LocalGLM(t, project)
	t.Setenv("EAGENT_BASE_URL", url)
	t.Setenv("EAGENT_API_KEY_ENV", "")
	s := &settings.Service{Project: project}
	res := config.Resolve(project, "", "")
	if res.Effective.Model.ContextTokens != 32_768 {
		t.Fatalf("window in force = %d", res.Effective.Model.ContextTokens)
	}
	cfg := res.Effective
	cfg.TickSeconds = 15
	raw, _ := json.Marshal(cfg)
	if _, err := s.Save(context.Background(), settings.Request{Config: raw}); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Clean(config.File(project)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), "context_tokens") || !strings.Contains(string(file), `"tick_seconds": 15`) && !strings.Contains(string(file), `"tick_seconds":15`) {
		t.Fatalf("file = %s", file)
	}
	os.Unsetenv("EAGENT_BASE_URL")
	os.Unsetenv("EAGENT_API_KEY_ENV")
	if back, err := config.Load(project, ""); err != nil || back.Model.ContextTokens != 200_000 {
		t.Fatalf("without the variable: window %d err %v", back.Model.ContextTokens, err)
	}
}
