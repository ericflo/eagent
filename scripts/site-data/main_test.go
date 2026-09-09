package main

import (
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

func TestCatalogFollowsNewPresetsAndRoutes(t *testing.T) {
	// A new preset must reach the website without edits to its HTML, JS, or
	// exporter. Exercise catalog labels, effort, and a nested fallback too.
	const name = "website-test-preset"
	previousModels := config.Models
	t.Cleanup(func() {
		delete(config.Presets, name)
		config.Models = previousModels
	})
	actor := config.Defaults().Task
	provider, ok := config.ProviderForURL(actor.BaseURL)
	if !ok {
		t.Fatal("default task provider is missing from catalog")
	}
	actor.Model = "test/model"
	actor.ReasoningEffort = "test-effort"
	config.Models = append(append([]config.Model(nil), config.Models...), config.Model{
		Provider: provider.ID, ID: actor.Model, Label: "New model from catalog",
	})
	fallback := config.Defaults().Narrator
	actor.Fallback = &fallback
	config.Presets[name] = func(cfg *config.Config) {
		cfg.Orchestrator = actor
		cfg.Description = "A new canonical description."
	}
	for _, p := range exportCatalog().Presets {
		if p.Name != name {
			continue
		}
		r := p.Routes[0]
		if p.Description != "A new canonical description." || r.Model != actor.Model || r.Label != "New model from catalog" || r.Effort != actor.ReasoningEffort || r.Provider != provider.Label {
			t.Fatalf("new preset was not exported faithfully: %+v", p)
		}
		if r.Fallback == nil || r.Fallback.Model != fallback.Model || r.Fallback.Effort != fallback.ReasoningEffort {
			t.Fatalf("missing or incorrect fallback: %+v", r.Fallback)
		}
		return
	}
	t.Fatal("new preset was omitted")
}

func TestDefaultsWithoutMatchingPreset(t *testing.T) {
	previous := config.Presets
	t.Cleanup(func() { config.Presets = previous })
	config.Presets = make(map[string]func(*config.Config), len(previous))
	for name := range previous {
		config.Presets[name] = func(cfg *config.Config) { cfg.Orchestrator.Model = "non-default" }
	}
	data := exportCatalog()
	if data.Default != "" || data.Presets[0].Name != "" || data.Presets[0].Routes[0].Model != config.Defaults().Orchestrator.Model {
		t.Fatal("defaults should remain available when no named preset matches")
	}
}
