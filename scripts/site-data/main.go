// Command site-data exports the public, built-in model configuration for the
// website. It never loads project configuration, environment values, or keys.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"

	"github.com/ericflo/eagent/internal/config"
)

type route struct {
	Actor    string `json:"actor,omitempty"`
	Model    string `json:"model"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
	Effort   string `json:"effort"`
	KeyEnv   string `json:"keyEnv,omitempty"`
	Fallback *route `json:"fallback,omitempty"`
}

type preset struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Routes      []route `json:"routes"`
}

type catalog struct {
	Default   string   `json:"default"`
	Defaults  []route  `json:"defaults"`
	Presets   []preset `json:"presets"`
	Providers []string `json:"providers"`
}

func publicRoute(actor config.Actor) route {
	r := route{Model: actor.Model, Label: actor.Model, Provider: actor.BaseURL, Effort: actor.ReasoningEffort, KeyEnv: actor.APIKeyEnv}
	if model, ok := config.Lookup(actor.BaseURL, actor.Model); ok {
		r.Label = model.Label
	}
	if provider, ok := config.ProviderForURL(actor.BaseURL); ok {
		r.Provider = provider.Label
	}
	if actor.Fallback != nil {
		fallback := publicRoute(*actor.Fallback)
		r.Fallback = &fallback
	}
	return r
}

func routes(cfg config.Config) []route {
	result := []route{publicRoute(cfg.Orchestrator), publicRoute(cfg.Task), publicRoute(cfg.Narrator)}
	for i, actor := range []string{"orchestrator", "task", "narrator"} {
		result[i].Actor = actor
	}
	return result
}

func exportCatalog() catalog {
	defaults := config.Defaults()
	result := catalog{Defaults: routes(defaults)}
	names := config.PresetNames()
	// Preserve the CLI's order, and include new presets even if its curated
	// display order has not yet been updated.
	seen := make(map[string]bool)
	for _, name := range names {
		seen[name] = true
	}
	var extra []string
	for name := range config.Presets {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	names = append(names, extra...)
	for _, name := range names {
		cfg := config.Defaults()
		config.Presets[name](&cfg)
		result.Presets = append(result.Presets, preset{name, config.PresetDescription(name), routes(cfg)})
		if result.Default == "" && reflect.DeepEqual(cfg, defaults) {
			result.Default = name
		}
	}
	// Defaults need not correspond to a named preset.
	if result.Default == "" {
		result.Presets = append([]preset{{"", "Built-in default configuration.", result.Defaults}}, result.Presets...)
	}
	for _, provider := range config.Providers {
		result.Providers = append(result.Providers, provider.Label)
	}
	return result
}

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(exportCatalog()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
