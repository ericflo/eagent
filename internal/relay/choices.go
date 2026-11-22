package relay

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ericflo/eagent/internal/config"
)

// Model and effort choices for the agents a relay runs. A profile without a
// model or effort passes none, so the agent starts with what its own
// configuration says (Codex's config.toml, Claude Code's settings.json);
// these read that configuration and the models each agent can run, so a
// picker can say what leaving the choice alone gives and offer the rest.

// ModelChoice is one model an agent can run.
type ModelChoice struct {
	ID, Label, Detail string
}

// standardEfforts are the levels accepted by the Claude and Antigravity CLIs.
var standardEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// codexEfforts are offered when Codex's model cache cannot be read: the
// levels its current models accept.
var codexEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// AgentDefaults is the model and reasoning effort an agent starts with when
// eagent passes neither: Codex's config.toml model and
// model_reasoning_effort, Claude Code's settings.json model and effortLevel
// (ANTHROPIC_MODEL overrides its model). Either is "" when not set there.
func AgentDefaults(agent string) (model, effort string) {
	switch agent {
	case "antigravity":
		return antigravityDefaults()
	case "codex":
		raw, err := os.ReadFile(filepath.Join(codexHome(), "config.toml"))
		if err != nil {
			return "", ""
		}
		top := tomlTop(string(raw))
		return top["model"], top["model_reasoning_effort"]
	case "claude":
		var s struct {
			Model       string `json:"model"`
			EffortLevel string `json:"effortLevel"`
		}
		if raw, err := os.ReadFile(filepath.Join(claudeHome(), "settings.json")); err == nil {
			_ = json.Unmarshal(raw, &s)
		}
		if m := strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")); m != "" {
			s.Model = m
		}
		return strings.TrimSpace(s.Model), strings.TrimSpace(s.EffortLevel)
	}
	return "", ""
}

// tomlTop reads the top-level string keys of a TOML file (those before its
// first table), which is all a model default needs.
func tomlTop(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) < 2 || (value[0] != '"' && value[0] != '\'') {
			continue
		}
		if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
			out[strings.TrimSpace(key)] = value[1 : 1+end]
		}
	}
	return out
}

// codexCache is the part of Codex's models_cache.json a picker needs: the
// models its own /model picker lists, best first, and the efforts each takes.
type codexCache struct {
	Models []struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Visibility  string `json:"visibility"`
		Levels      []struct {
			Effort string `json:"effort"`
		} `json:"supported_reasoning_levels"`
	} `json:"models"`
}

func readCodexCache() (codexCache, bool) {
	var c codexCache
	raw, err := os.ReadFile(filepath.Join(codexHome(), "models_cache.json"))
	if err != nil || json.Unmarshal(raw, &c) != nil || len(c.Models) == 0 {
		return c, false
	}
	return c, true
}

// AgentModels lists the models an agent can be started with, best first:
// for Codex, the ones its own picker lists (from its model cache, else the
// OpenAI models in eagent's catalog); for Claude Code, its aliases for the
// latest of each family, then the Anthropic models eagent knows.
func AgentModels(agent, project string) []ModelChoice {
	var out []ModelChoice
	seen := map[string]bool{}
	add := func(c ModelChoice) {
		if c.ID != "" && !seen[c.ID] && len(out) < 100 {
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	switch agent {
	case "antigravity":
		return antigravityModels()
	case "codex":
		if c, ok := readCodexCache(); ok {
			for _, m := range c.Models {
				if m.Visibility == "list" {
					add(ModelChoice{ID: m.Slug, Label: m.DisplayName, Detail: m.Description})
				}
			}
			return out
		}
		for _, m := range config.Models {
			if m.Provider == "openai" {
				add(ModelChoice{ID: m.ID, Label: m.Label})
			}
		}
	case "claude":
		for _, a := range []struct{ id, label string }{{"fable", "Fable"}, {"opus", "Opus"}, {"sonnet", "Sonnet"}, {"haiku", "Haiku"}} {
			add(ModelChoice{ID: a.id, Label: a.label + " (latest)", Detail: "Claude Code's alias for the newest " + a.label})
		}
		var models []config.Model
		for _, m := range config.KnownModels(project) {
			if m.Provider == "anthropic" {
				models = append(models, m)
			}
		}
		// By family as the aliases are, newest first within each.
		slices.SortStableFunc(models, func(a, b config.Model) int {
			fa, va := claudeVersion(a.ID)
			fb, vb := claudeVersion(b.ID)
			if fa != fb {
				return fa - fb
			}
			return slices.Compare(vb, va)
		})
		for _, m := range models {
			label := m.Label
			if label == m.ID {
				label = ""
			}
			add(ModelChoice{ID: m.ID, Label: label})
		}
	}
	return out
}

// claudeVersion reads "claude-opus-4-5-20251101" as the Opus family (by the
// aliases' order, unknown ones last) and version [4 5 20251101].
func claudeVersion(id string) (family int, version []int) {
	parts := strings.Split(strings.TrimPrefix(id, "claude-"), "-")
	family = slices.Index([]string{"fable", "opus", "sonnet", "haiku"}, parts[0])
	if family < 0 {
		family = 4
	}
	for _, p := range parts[1:] {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return family, version
			}
			n = n*10 + int(c-'0')
		}
		version = append(version, n)
	}
	return family, version
}

// AgentEfforts lists the reasoning efforts an agent accepts, in order: for
// Codex, every level its listed models take.
func AgentEfforts(agent string) []string {
	switch agent {
	case "antigravity", "claude":
		return slices.Clone(standardEfforts)
	case "codex":
		c, ok := readCodexCache()
		if !ok {
			return slices.Clone(codexEfforts)
		}
		var out []string
		for _, m := range c.Models {
			if m.Visibility != "list" {
				continue
			}
			for _, l := range m.Levels {
				if l.Effort != "" && len(l.Effort) <= 40 && !slices.Contains(out, l.Effort) {
					out = append(out, l.Effort)
				}
			}
		}
		if len(out) == 0 {
			return slices.Clone(codexEfforts)
		}
		// Mildest first: eagent's scale, then Codex's "ultra" (max with
		// delegation), then any level neither knows, as Codex lists them.
		scale := append(slices.Clone(config.EffortOrder), "ultra")
		rank := func(e string) int {
			if i := slices.Index(scale, e); i >= 0 {
				return i
			}
			return len(scale) + slices.Index(out, e)
		}
		ranked := slices.Clone(out)
		slices.SortFunc(ranked, func(a, b string) int { return rank(a) - rank(b) })
		return ranked
	}
	return nil
}

// AgentName is how people call an agent ("Claude Code", "Codex").
func AgentName(agent string) string {
	switch agent {
	case "antigravity":
		return "Antigravity"
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	}
	return agent
}

// ModelLabel is how a model reads for an agent: its picker label, else its id.
func ModelLabel(agent, project, model string) string {
	for _, c := range AgentModels(agent, project) {
		if c.ID == model && c.Label != "" {
			if agent == "claude" {
				return strings.TrimPrefix(strings.TrimSuffix(c.Label, " (latest)"), "Claude ")
			}
			return c.Label
		}
	}
	return model
}

// ProfileDefaults resolves a relay's overrides against its native defaults.
func ProfileDefaults(agent, model, effort string) (string, string) {
	if agent == "antigravity" && model != "" {
		id, variant := agyModelVariant(model)
		return id, cmp.Or(effort, variant)
	}
	defModel, defEffort := AgentDefaults(agent)
	return cmp.Or(model, defModel), cmp.Or(effort, defEffort)
}

// Label names a relay for people with what it runs: "Codex · GPT-6.2-Astra
// xhigh". model and effort are what it starts with ("" for the agent's own
// default, read from its configuration where it says).
func Label(agent, project, model, effort string) string {
	model, effort = ProfileDefaults(agent, model, effort)
	var parts []string
	if model != "" {
		label := ModelLabel(agent, project, model)
		if agent == "antigravity" && effort != "" {
			if stem, suffix, ok := strings.Cut(label, " ("); ok && slices.Contains(standardEfforts, strings.ToLower(strings.TrimSuffix(suffix, ")"))) {
				label = stem
			}
		}
		parts = append(parts, label)
	}
	if effort != "" {
		parts = append(parts, effort)
	}
	if len(parts) == 0 {
		return AgentName(agent)
	}
	return AgentName(agent) + " · " + strings.Join(parts, " ")
}
