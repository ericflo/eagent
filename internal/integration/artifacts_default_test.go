package integration

import (
	"context"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/event"
)

// Archive publication is on by default; an explicit `artifacts: false` or
// EAGENT_FINALECHAT_ARTIFACTS=0/false opts out.
func TestArtifactsDefaultOnWithOptOut(t *testing.T) {
	if !config.Defaults().Finalechat.Artifacts {
		t.Fatal("Defaults() must enable artifact publication")
	}
	project := t.TempDir()
	t.Setenv("EAGENT_FINALECHAT_ARTIFACTS", "")
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Finalechat.Artifacts {
		t.Fatal("default config must publish archives")
	}
	if !IsArtifactEnabled(project) {
		t.Fatal("IsArtifactEnabled must be true with no project file")
	}
	// A file that does not mention artifacts keeps the default.
	if err := writeJSONAtomic(config.File(project), map[string]any{"finalechat": map[string]any{"base_url": "https://example.invalid"}}); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Finalechat.Artifacts {
		t.Fatal("omitted artifacts key must keep publication enabled")
	}
	// Explicit false opts out.
	if err := writeJSONAtomic(config.File(project), map[string]any{"finalechat": map[string]any{"artifacts": false}}); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Finalechat.Artifacts {
		t.Fatal("explicit artifacts:false must disable publication")
	}
	if IsArtifactEnabled(project) {
		t.Fatal("IsArtifactEnabled must be false with artifacts:false")
	}
	// Environment opt-outs and opt-ins win over the file.
	for _, value := range []string{"0", "false"} {
		t.Setenv("EAGENT_FINALECHAT_ARTIFACTS", value)
		cfg, err = config.Load(project, "")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Finalechat.Artifacts {
			t.Fatalf("EAGENT_FINALECHAT_ARTIFACTS=%q must disable publication", value)
		}
	}
	for _, value := range []string{"1", "true"} {
		t.Setenv("EAGENT_FINALECHAT_ARTIFACTS", value)
		cfg, err = config.Load(project, "")
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Finalechat.Artifacts {
			t.Fatalf("EAGENT_FINALECHAT_ARTIFACTS=%q must enable publication", value)
		}
	}
}

// The default configuration publishes without force; explicit false makes
// non-forced Publish a no-op without touching the network.
func TestPublisherPublishesByDefaultAndRespectsOptOut(t *testing.T) {
	f, project, session := newPublicationFixture(t)
	cfg, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	// Drop the explicit opt-in the fixture writes: the default alone must publish.
	if err := writeJSONAtomic(config.File(project), map[string]any{"finalechat": map[string]any{"enabled": false, "base_url": cfg.Finalechat.BaseURL}}); err != nil {
		t.Fatal(err)
	}
	if !IsArtifactEnabled(project) {
		t.Fatal("default config must enable the publisher")
	}
	// Automatic publication waits for the phone mirror's first Post (which
	// creates the thread with its title); record it so registration may go.
	if _, err := session.Append(event.New(event.PhoneThread, event.ActorHarness, event.PhoneThreadData{ThreadID: "fixture-thread", ExternalID: "eagent:" + session.ID, BaseURL: cfg.Finalechat.BaseURL})); err != nil {
		t.Fatal(err)
	}
	id, err := Publish(context.Background(), project, session.ID, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("default Publish did not publish")
	}
	f.mu.Lock()
	commits := f.commits
	calls := f.calls
	f.mu.Unlock()
	if commits != 1 {
		t.Fatalf("default Publish made %d commits", commits)
	}
	// Explicit opt-out: non-forced Publish is a silent no-op.
	if err := writeJSONAtomic(config.File(project), map[string]any{"finalechat": map[string]any{"artifacts": false, "enabled": false, "base_url": cfg.Finalechat.BaseURL}}); err != nil {
		t.Fatal(err)
	}
	id, err = Publish(context.Background(), project, session.ID, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("opted-out Publish uploaded %q", id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commits != commits || f.calls != calls {
		t.Fatal("opted-out Publish made network requests")
	}
}
