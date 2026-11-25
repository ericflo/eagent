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

func publicRoute(m config.ModelRoute) route {
	r := route{Model: m.Model, Label: m.Model, Provider: m.BaseURL, Effort: m.ReasoningEffort, KeyEnv: m.APIKeyEnv}
	if model, ok := config.Lookup(m.BaseURL, m.Model); ok {
		r.Label = model.Label
	}
	if provider, ok := config.ProviderForURL(m.BaseURL); ok {
		r.Provider = provider.Label
	}
	if m.Fallback != nil {
		fallback := publicRoute(*m.Fallback)
		r.Fallback = &fallback
	}
	return r
}

// routes lists the models a configuration runs: eagent's, and a dedicated
// summarizer's when it has its own.
func routes(cfg config.Config) []route {
	main := publicRoute(cfg.Model)
	main.Actor = "eagent"
	result := []route{main}
	if cfg.Summarizer.Mode == "dedicated" && cfg.Summarizer.Model != nil && cfg.Summarizer.Model.Model != "" {
		s := publicRoute(*cfg.Summarizer.Model)
		s.Actor = "summarizer"
		result = append(result, s)
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
