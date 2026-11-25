// Package config resolves eagent's settings from defaults, an optional
// preset, the project's .agents/eagent/config.json, and EAGENT_* environment
// variables, in that order. Credentials are only ever read from the
// environment and are never written anywhere.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ericflo/eagent/internal/boundedfile"
	"github.com/ericflo/eagent/internal/filelock"
	"github.com/ericflo/eagent/internal/llm"
)

// ModelRoute configures one model endpoint.
type ModelRoute struct {
	Protocol  string `json:"protocol"` // openai-chat | openai-responses | anthropic
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKeyEnv string `json:"api_key_env"`
	// ReasoningEffort is the one effort every call of a session runs at:
	// looks, decisions and the dossier alike. It never changes from call
	// to call, because on many engines the effort is part of the rendered
	// prompt (a line of the system block, a thinking setting), and a
	// change would discard the provider's cache of the conversation.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ReplayReasoning sends models their prior reasoning back on later
	// calls (encrypted reasoning on OpenAI Responses; reasoning_content /
	// reasoning on OpenAI Chat Completions) to preserve tool-loop continuity
	// and prefix caching. On by default; false trades continuity for smaller
	// prompts.
	ReplayReasoning *bool `json:"replay_reasoning,omitempty"`
	MaxTokens       int   `json:"max_tokens,omitempty"`
	// ContextTokens is the model's usable window. The session starts a new
	// subsession long before it, at RolloverTokens.
	ContextTokens int `json:"context_tokens,omitempty"`
	// Fallback is tried when the primary route is unroutable (bad key, no
	// access to the model, exhausted credits).
	Fallback *ModelRoute `json:"fallback,omitempty"`
}

// Summarizer modes.
const (
	// SummarizerInline has eagent's own loop keep the thread's title and
	// summary, with the update_thread tool, on the prompt it already has
	// cached.
	SummarizerInline = "inline"
	// SummarizerDedicated makes a separate small call for them, on a route
	// of its own.
	SummarizerDedicated = "dedicated"
)

// Summarizer says how the thread's title and summary are written.
type Summarizer struct {
	// Mode is "inline" (the default) or "dedicated".
	Mode string `json:"mode"`
	// Model is the route a dedicated summarizer calls, exactly as written
	// here: nothing about it is adjusted behind the scenes. Left out, a
	// dedicated summarizer uses the main model's route. Ignored when inline.
	Model *ModelRoute `json:"model,omitempty"`
}

// Route is the route a dedicated summarizer calls: its own, else main.
func (s Summarizer) Route(main ModelRoute) ModelRoute {
	if s.Model != nil && s.Model.Model != "" {
		return *s.Model
	}
	return main
}

// Config is the full effective configuration.
type Config struct {
	// Name is the bundle this configuration came from ("" for the project
	// default). Description is free text shown by `eagent config list`.
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// DefaultConfig, set in the project config.json, selects a bundle when
	// neither --config nor EAGENT_CONFIG is given.
	DefaultConfig string `json:"default_config,omitempty"`

	Preset string `json:"preset,omitempty"`
	// Model is the one model eagent runs on: it reads what the agent does,
	// talks to the user, and drives the agent's terminal.
	Model ModelRoute `json:"model"`
	// Summarizer is how the thread's title and summary are kept.
	Summarizer Summarizer `json:"summarizer"`

	// MaxCallsPerTurn bounds the model calls one wake may make (a look at
	// the screen, a key press, another look, ...).
	MaxCallsPerTurn int `json:"max_calls_per_turn"`
	// TickSeconds is how often eagent considers saying something while the
	// agent works. A tick with nothing new costs no model call.
	TickSeconds int `json:"tick_seconds"`
	// QuietSeconds is the longest the user goes without a progress line
	// while the agent works. 0 disables the limit.
	QuietSeconds int `json:"quiet_seconds"`
	// RolloverTokens is the prompt size at which the conversation is
	// restarted from a dossier in a new subsession.
	RolloverTokens int `json:"rollover_tokens"`
	// ReportMaxChars bounds how much of an agent's turn report eagent's own
	// prompt carries. The user always gets the whole report.
	ReportMaxChars int `json:"report_max_chars"`
	// Persona overrides eagent's voice.
	Persona string `json:"persona,omitempty"`
	// Finalechat mirrors the conversation to the user's phone. It is on
	// whenever a token is found unless enabled is explicitly false.
	Finalechat Finalechat `json:"finalechat"`
	// Relay configures the coding agents eagent can run.
	Relay Relay `json:"relay,omitzero"`
	// Instructions is loaded from .agents/eagent/INSTRUCTIONS.md: notes for
	// eagent itself. (The agent reads the project's AGENTS.md on its own.)
	Instructions string `json:"-"`
	// Migrated lists what was carried over from keys of the three-actor
	// design when this configuration was read, for `eagent doctor`.
	Migrated []string `json:"-"`
}

// Relay configures relay sessions. Profiles are kept in a map so the remote
// settings editor (which walks scalar fields) never offers them: a profile
// names a program to run.
type Relay struct {
	// Profiles are named ways to start an agent. The built-in ones
	// (BuiltinRelayProfiles) are always available; a profile here with the
	// same name replaces the built-in one.
	Profiles map[string]RelayProfile `json:"profiles,omitempty"`
	// Default names the profile a session starts when it is not told which.
	// Empty picks the first built-in agent that is installed.
	Default string `json:"default,omitempty"`
}

// RelayProfile is one way to start an external agent.
type RelayProfile struct {
	// Agent is the relay agent, by the name its adapter registers in the
	// relay package: "antigravity", "claude", "codex" or "pi".
	Agent string `json:"agent"`
	// Label is shown on the phone ("Codex"); empty reads as the agent's name
	// with the model it runs.
	Label string `json:"label,omitempty"`
	// Model and Effort are passed to the agent (codex -m and
	// model_reasoning_effort, claude --model and --effort, pi --model and
	// --thinking). Empty passes
	// nothing, so the agent uses its own configured default. A session may
	// pick others when it starts the relay, and change them while it runs.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Args are extra command-line arguments, after the ones eagent adds.
	Args []string `json:"args,omitempty"`
	// Host is the terminal multiplexer: "tmux" or "screen"; empty picks tmux
	// when it is installed.
	Host string `json:"host,omitempty"`
	// GitPull fast-forwards the working directory's branch before starting.
	GitPull bool `json:"git_pull,omitempty"`
}

// BuiltinRelayProfiles are the profiles every project has. They name no
// model or effort: each agent starts with the defaults in its own
// configuration (~/.codex/config.toml, ~/.claude/settings.json), and a
// session can choose others.
func BuiltinRelayProfiles() map[string]RelayProfile {
	return map[string]RelayProfile{
		"antigravity": {Agent: "antigravity", Label: "Antigravity", Args: []string{"--dangerously-skip-permissions"}},
		"codex":       {Agent: "codex", Label: "Codex", Args: []string{"--yolo"}},
		"claude":      {Agent: "claude", Label: "Claude Code", Args: []string{"--dangerously-skip-permissions"}},
		"pi":          {Agent: "pi", Label: "Pi"},
	}
}

// With is the profile with a session's picks over its model and effort
// ("" keeps the profile's own).
func (p RelayProfile) With(model, effort string) RelayProfile {
	if model != "" {
		p.Model = model
	}
	if effort != "" {
		p.Effort = effort
	}
	return p
}

// RelayProfiles merges the built-in profiles with the configured ones.
func (r Relay) RelayProfiles() map[string]RelayProfile {
	out := BuiltinRelayProfiles()
	for name, p := range r.Profiles {
		out[name] = p
	}
	return out
}

// RelayProfileNames lists the profiles, sorted.
func (r Relay) RelayProfileNames() []string {
	var names []string
	for name := range r.RelayProfiles() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Finalechat configures the phone mirror (https://www.finalechat.com).
type Finalechat struct {
	// Artifacts enables proactive upload of native session logs and portable
	// viewers, independently of conversation mirroring. On by default; set
	// false (or EAGENT_FINALECHAT_ARTIFACTS=0) to opt out.
	Artifacts bool `json:"artifacts,omitempty"`
	// Enabled: nil means "on when a token is found"; false turns it off even
	// when a token is present; true makes a missing token an error at start.
	Enabled *bool `json:"enabled,omitempty"`
	// TokenEnv is the environment variable holding the fc_ token. The
	// finalechat CLI's ~/.config/finalechat/config.json is the fallback.
	TokenEnv string `json:"token_env,omitempty"`
	// BaseURL overrides the service address (self-hosted or staging).
	BaseURL string `json:"base_url,omitempty"`
	// Agent is the name shown next to the thread in the app.
	Agent string `json:"agent,omitempty"`
	// QuestionTimeoutSeconds is how long a phone question stays open. A
	// batch (-p) session with a pending question waits this long for the
	// phone before ending as awaiting-input.
	QuestionTimeoutSeconds int `json:"question_timeout_seconds,omitempty"`
	// MirrorInput copies what the user types in the terminal or web UI into
	// the thread so the phone shows the whole conversation. Default true.
	MirrorInput *bool `json:"mirror_input,omitempty"`
}

// Wanted reports whether the mirror should be attempted: on unless disabled.
func (f Finalechat) Wanted() bool { return f.Enabled == nil || *f.Enabled }

// Required reports whether a missing token is an error (enabled: true).
func (f Finalechat) Required() bool { return f.Enabled != nil && *f.Enabled }

// TokenEnvName is the environment variable to read.
func (f Finalechat) TokenEnvName() string {
	if f.TokenEnv != "" {
		return f.TokenEnv
	}
	return "FINALECHAT_TOKEN"
}

// AgentName is the name shown in the app.
func (f Finalechat) AgentName() string {
	if f.Agent != "" {
		return f.Agent
	}
	return "eagent"
}

// QuestionTimeout is the question lifetime.
func (f Finalechat) QuestionTimeout() int {
	if f.QuestionTimeoutSeconds > 0 {
		return f.QuestionTimeoutSeconds
	}
	return 3600
}

// Mirror reports whether terminal and web input is copied to the phone.
func (f Finalechat) Mirror() bool { return f.MirrorInput == nil || *f.MirrorInput }

const (
	togetherURL   = "https://api.together.xyz/v1"
	openaiURL     = "https://api.openai.com/v1"
	openrouterURL = "https://openrouter.ai/api/v1"
	anthropicURL  = "https://api.anthropic.com/v1"
	deepinfraURL  = "https://api.deepinfra.com/v1/openai"
	fireworksURL  = "https://api.fireworks.ai/inference/v1"
	zenURL        = "https://opencode.ai/zen/v1"
	nousURL       = "https://inference-api.nousresearch.com/v1"
)

// loopMaxTokens is the output ceiling of every call: room to think and to
// write a dossier of a few pages. It is the same for every call, like the
// effort, because some engines derive a thinking budget from it. A ceiling
// costs nothing when a reply is short.
const loopMaxTokens = 32768

// loopEffort is the effort the presets run at. One model both watches the
// agent (often, where low would do) and decides for the user (a stuck
// terminal, autopilot, where the three-actor design used its highest
// effort), and it has one effort for both.
const loopEffort = "medium"

// glmDescription describes the built-in defaults, which the "glm" preset names.
const glmDescription = "GLM-5.3 (Together AI)"

// Defaults is the shipped configuration: GLM-5.3 on Together AI, thinking a
// little, with the thread's title and summary kept inline.
func Defaults() Config {
	return Config{
		Model:           chatRoute(togetherURL, "TOGETHER_API_KEY", "zai-org/GLM-5.3", 200_000),
		Summarizer:      Summarizer{Mode: SummarizerInline},
		MaxCallsPerTurn: 30,
		TickSeconds:     30,
		QuietSeconds:    180,
		RolloverTokens:  64_000,
		ReportMaxChars:  6000,
		Finalechat:      Finalechat{Artifacts: true},
	}
}

// Presets are named models to run eagent on. The provider-tier names
// (openai-high, openrouter-med, anthropic-med, ...) trade cost for capability
// within one provider; glm is the default.
//
// Every preset runs at one effort, medium (see loopEffort), for every call.
// It always thinks, never none.
var Presets = map[string]func(*Config){
	"glm": func(c *Config) {}, // the defaults
	"astra": func(c *Config) {
		c.Description = "GPT-6 Astra (OpenAI, OpenRouter fallback)"
		c.Model = openaiRoute("gpt-6-astra")
	},
	"openai-high": func(c *Config) {
		c.Description = "GPT-6 Astra (OpenAI, OpenRouter fallback)"
		c.Model = openaiRoute("gpt-6-astra")
	},
	"openai-med": func(c *Config) {
		c.Description = "GPT-6.1 Sol (OpenAI, OpenRouter fallback)"
		c.Model = openaiRoute("gpt-6.1-sol")
	},
	"openai-low": func(c *Config) {
		c.Description = "GPT-6 Luna (OpenAI, OpenRouter fallback)"
		c.Model = openaiRoute("gpt-6-luna")
	},
	"openrouter-high": func(c *Config) {
		c.Description = "Kimi K3 (OpenRouter)"
		c.Model = chatRoute(openrouterURL, "OPENROUTER_API_KEY", "moonshotai/kimi-k3", 1_000_000)
	},
	"openrouter-med": func(c *Config) {
		c.Description = "GLM-5.3 (OpenRouter)"
		c.Model = chatRoute(openrouterURL, "OPENROUTER_API_KEY", "z-ai/glm-5.3", 200_000)
	},
	"openrouter-low": func(c *Config) {
		c.Description = "GLM-5.3-Flash (OpenRouter; cheapest)"
		c.Model = chatRoute(openrouterURL, "OPENROUTER_API_KEY", "z-ai/glm-5.3-flash", 200_000)
	},
	"anthropic-high": func(c *Config) {
		c.Description = "Claude Fable 5.1"
		c.Model = anthropicRoute("claude-fable-5-1")
	},
	"anthropic-med": func(c *Config) {
		c.Description = "Claude Opus 5.5"
		c.Model = anthropicRoute("claude-opus-5-5")
	},
	"deepinfra-high": func(c *Config) {
		c.Description = "Kimi K3 (DeepInfra)"
		c.Model = chatRoute(deepinfraURL, "DEEPINFRA_API_KEY", "moonshotai/Kimi-K3", 1_000_000)
	},
	"deepinfra-med": func(c *Config) {
		c.Description = "GLM-5.3 (DeepInfra)"
		c.Model = chatRoute(deepinfraURL, "DEEPINFRA_API_KEY", "zai-org/GLM-5.3", 200_000)
	},
	"deepinfra-low": func(c *Config) {
		c.Description = "GLM-5.3-Flash (DeepInfra; cheapest)"
		c.Model = chatRoute(deepinfraURL, "DEEPINFRA_API_KEY", "zai-org/GLM-5.3-Flash", 200_000)
	},
	"fireworks-high": func(c *Config) {
		c.Description = "Kimi K3 (Fireworks)"
		c.Model = chatRoute(fireworksURL, "FIREWORKS_API_KEY", "accounts/fireworks/models/kimi-k3", 1_000_000)
	},
	"fireworks-med": func(c *Config) {
		c.Description = "GLM-5.3 (Fireworks)"
		c.Model = chatRoute(fireworksURL, "FIREWORKS_API_KEY", "accounts/fireworks/models/glm-5p3", 200_000)
	},
	"fireworks-low": func(c *Config) {
		c.Description = "GLM-5.3-Flash (Fireworks; cheapest)"
		c.Model = chatRoute(fireworksURL, "FIREWORKS_API_KEY", "accounts/fireworks/models/glm-5p3-flash", 200_000)
	},
	"opencode-high": func(c *Config) {
		c.Description = "Claude Fable 5.1 (OpenCode Zen)"
		c.Model = ModelRoute{Protocol: llm.ProtocolAnthropic, BaseURL: zenURL, Model: "claude-fable-5-1", APIKeyEnv: "OPENCODE_ZEN_API_KEY", ReasoningEffort: loopEffort, MaxTokens: loopMaxTokens, ContextTokens: 1_000_000}
	},
	"opencode-med": func(c *Config) {
		c.Description = "GLM-5.3 (OpenCode Zen)"
		c.Model = chatRoute(zenURL, "OPENCODE_ZEN_API_KEY", "glm-5.3", 200_000)
	},
	"opencode-low": func(c *Config) {
		c.Description = "GLM-5.3-Flash (OpenCode Zen; cheapest)"
		c.Model = chatRoute(zenURL, "OPENCODE_ZEN_API_KEY", "glm-5.3-flash", 200_000)
	},
	"nous-high": func(c *Config) {
		c.Description = "Kimi K3 (Nous Portal)"
		c.Model = chatRoute(nousURL, "NOUS_API_KEY", "moonshotai/kimi-k3", 1_000_000)
	},
	"nous-med": func(c *Config) {
		c.Description = "GLM-5.3 (Nous Portal)"
		c.Model = chatRoute(nousURL, "NOUS_API_KEY", "z-ai/glm-5.3", 200_000)
	},
	"nous-low": func(c *Config) {
		c.Description = "GLM-5.3-Flash (Nous Portal; cheapest)"
		c.Model = chatRoute(nousURL, "NOUS_API_KEY", "z-ai/glm-5.3-flash", 200_000)
	},
	"deepseek": func(c *Config) {
		c.Description = "DeepSeek V4.1 Flash 1017 (Together; cheap; the shape of a single local model)"
		c.Model = chatRoute(togetherURL, "TOGETHER_API_KEY", "deepseek-ai/DeepSeek-V4.1-Flash-1017", 1_048_576)
	},
	"muse": func(c *Config) {
		c.Description = "Muse Spark 1.3 Contributor (Meta)"
		c.Model = ModelRoute{Protocol: llm.ProtocolResponses, BaseURL: metaURL, Model: "muse-spark-1.3-contributor", APIKeyEnv: "META_API_KEY", ReasoningEffort: loopEffort, MaxTokens: loopMaxTokens, ContextTokens: 1_000_000}
	},
	"qwen": func(c *Config) {
		c.Description = "Qwen 3.8 27B (OpenRouter; a dense model you could run locally)"
		c.Model = chatRoute(openrouterURL, "OPENROUTER_API_KEY", "qwen/qwen3.8-27b", 200_000)
	},
	"mistral": func(c *Config) {
		c.Description = "Mistral Large 4 (OpenRouter; 1M context)"
		c.Model = chatRoute(openrouterURL, "OPENROUTER_API_KEY", "mistralai/mistral-large-4-0", 1_048_576)
	},
	"muse-open": func(c *Config) {
		c.Description = "Muse Spark Open, Meta's open-weight model (OpenRouter; 1M context)"
		c.Model = chatRoute(openrouterURL, "OPENROUTER_API_KEY", "meta/muse-spark-open", 1_048_576)
	},
	"beam": func(c *Config) {
		c.Description = "Reflection Beam 1.0 501B A23B (OpenRouter; 1M context, thinking always on)"
		c.Model = chatRoute(openrouterURL, "OPENROUTER_API_KEY", "reflection/beam-1.0-501b-a23b", 1_048_576)
	},
	"deepseek-1017": func(c *Config) {
		c.Description = "DeepSeek V4.1 Flash 1017 (OpenRouter; 1M context, weekday peak hours cost double)"
		c.Model = chatRoute(openrouterURL, "OPENROUTER_API_KEY", "deepseek/deepseek-v4.1-flash-1017", 1_048_576)
	},
}

// presetAliases keeps older names working.
var presetAliases = map[string]string{"anthropic": "anthropic-high"}

// PresetNames lists presets in a stable order.
func PresetNames() []string {
	return []string{
		"glm", "astra",
		"openai-high", "openai-med", "openai-low",
		"openrouter-high", "openrouter-med", "openrouter-low",
		"anthropic-high", "anthropic-med",
		"deepinfra-high", "deepinfra-med", "deepinfra-low",
		"fireworks-high", "fireworks-med", "fireworks-low",
		"opencode-high", "opencode-med", "opencode-low",
		"nous-high", "nous-med", "nous-low",
		"deepseek", "qwen", "muse",
		"mistral", "muse-open", "beam", "deepseek-1017",
	}
}

// ResolvePreset maps a preset name or alias to its canonical name; ok is
// false for unknown names.
func ResolvePreset(name string) (string, bool) {
	if canon, ok := presetAliases[name]; ok {
		name = canon
	}
	_, ok := Presets[name]
	return name, ok
}

// PresetDescription returns the one-line description of a built-in preset.
func PresetDescription(name string) string {
	c := Defaults()
	if name == "glm" {
		return glmDescription
	}
	if canon, ok := ResolvePreset(name); ok {
		Presets[canon](&c)
	}
	return c.Description
}

// openaiRoute is a model on OpenAI's Responses API, with the same model
// through OpenRouter when the direct route is unroutable (no key, no access,
// no credits).
func openaiRoute(model string) ModelRoute {
	return ModelRoute{
		Protocol: llm.ProtocolResponses, BaseURL: openaiURL, Model: model,
		APIKeyEnv: "OPENAI_API_KEY", ReasoningEffort: loopEffort, MaxTokens: loopMaxTokens, ContextTokens: 1_050_000,
		Fallback: &ModelRoute{
			Protocol: llm.ProtocolResponses, BaseURL: openrouterURL, Model: "openai/" + model,
			APIKeyEnv: "OPENROUTER_API_KEY", ReasoningEffort: loopEffort, MaxTokens: loopMaxTokens, ContextTokens: 1_050_000,
		},
	}
}

// chatRoute is an OpenAI-compatible chat route on any provider.
func chatRoute(baseURL, keyEnv, model string, contextTokens int) ModelRoute {
	return ModelRoute{
		Protocol: llm.ProtocolChat, BaseURL: baseURL, Model: model,
		APIKeyEnv: keyEnv, ReasoningEffort: loopEffort, MaxTokens: loopMaxTokens, ContextTokens: contextTokens,
	}
}

func anthropicRoute(model string) ModelRoute {
	return ModelRoute{
		Protocol: llm.ProtocolAnthropic, BaseURL: anthropicURL, Model: model,
		APIKeyEnv: "ANTHROPIC_API_KEY", ReasoningEffort: loopEffort, MaxTokens: loopMaxTokens, ContextTokens: 1_000_000,
	}
}

// BundlesDir holds a project's named configurations.
func BundlesDir(project string) string { return filepath.Join(project, ".agents", "eagent", "configs") }

// BundlePath is the file for a named configuration.
func BundlePath(project, name string) string { return filepath.Join(BundlesDir(project), name+".json") }

// ListBundles returns the named configurations in a project, sorted.
func ListBundles(project string) ([]string, error) {
	entries, err := os.ReadDir(BundlesDir(project))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(names)
	return names, nil
}

// SaveBundle writes cfg as a named configuration, overwriting any existing one.
func SaveBundle(project, name, description string, cfg Config) (string, error) {
	editor, err := LockEditor(context.Background(), project)
	if err != nil {
		return "", err
	}
	defer editor.Close()
	return editor.SaveBundle(name, description, cfg)
}

// DeleteBundle removes a saved configuration from the project unless it is
// currently the project's default_config.
func DeleteBundle(project, name string) error {
	if !validName(name) {
		return fmt.Errorf("config bundle names use letters, digits, '-', '_' and '.' only")
	}
	editor, err := LockEditor(context.Background(), project)
	if err != nil {
		return err
	}
	defer editor.Close()
	res := Resolve(project, "", "")
	if res.Effective.DefaultConfig == name {
		return fmt.Errorf("select a different default before deleting %q", name)
	}
	if _, err := os.Stat(BundlePath(project, name)); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no saved preset %q in %s", name, BundlesDir(project))
	}
	_, err = editor.WriteRelated("bundle", name, nil, true)
	return err
}

// BundleBytes prepares a named configuration without writing it. Remote
// connectors can journal this exact intent before crossing the writer boundary.
func BundleBytes(name, description string, cfg Config) ([]byte, error) {
	if !validName(name) {
		return nil, fmt.Errorf("bundle names use letters, digits, '-', '_' and '.' only")
	}
	if _, builtIn := ResolvePreset(name); builtIn {
		return nil, fmt.Errorf("%q is a built-in preset name; choose a different name", name)
	}
	cfg.Name = name
	if description != "" {
		cfg.Description = description
	}
	cfg.DefaultConfig = ""
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	// Spell out "fallback": null when the model has no fallback so loading
	// this saved preset on top of a project file whose preset carries one
	// (such as openai-high) clears it instead of inheriting it.
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if cfg.Model.Fallback == nil {
		if m, ok := obj["model"].(map[string]any); ok {
			m["fallback"] = nil
		}
	}
	pretty, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(pretty, '\n'), nil
}

// ValidBundleName reports whether name is a safe saved preset / bundle name.
func ValidBundleName(name string) bool { return validName(name) }

func validName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// File is the per-project override path.
func File(project string) string { return filepath.Join(project, ".agents", "eagent", "config.json") }

// Load resolves the configuration for a project directory.
//
// Layers, each overriding the previous: built-in defaults, a preset (from
// --preset, EAGENT_PRESET, the bundle, or config.json), the project's
// config.json, a named bundle from .agents/eagent/configs/<name>.json
// (from --config, EAGENT_CONFIG, or config.json's default_config), and
// EAGENT_* environment variables.
func Load(project string, preset string) (Config, error) {
	return LoadBundle(project, preset, "")
}

// LoadBundle is Load with an explicit bundle name ("" = none selected).
func LoadBundle(project, preset, bundle string) (Config, error) {
	fileCfg, err := readJSONMap(File(project))
	if err != nil {
		return Defaults(), err
	}
	return LoadBundleWithFile(project, preset, bundle, fileCfg)
}

// LoadBundleWithFile resolves proposed file contents through the actual
// resolver without writing them or changing process environment variables.
func LoadBundleWithFile(project, preset, bundle string, fileCfg map[string]json.RawMessage) (Config, error) {
	cfg := Defaults()
	var err error
	// Explicit --preset / EAGENT_PRESET may name either a built-in preset or a
	// saved preset in .agents/eagent/configs/<name>.json.
	explicitPreset := preset
	if explicitPreset == "" {
		explicitPreset = os.Getenv("EAGENT_PRESET")
	}
	if bundle == "" && explicitPreset != "" {
		if _, isBuiltIn := ResolvePreset(explicitPreset); !isBuiltIn && validName(explicitPreset) {
			if _, statErr := os.Stat(BundlePath(project, explicitPreset)); statErr == nil {
				bundle = explicitPreset
				preset = ""
				explicitPreset = ""
			}
		}
	}
	if bundle == "" {
		bundle = os.Getenv("EAGENT_CONFIG")
	}
	if bundle == "" {
		if raw, ok := fileCfg["default_config"]; ok {
			_ = json.Unmarshal(raw, &bundle)
		}
	}
	var bundleCfg map[string]json.RawMessage
	if bundle != "" {
		// A bundle name may also be a built-in preset.
		if canon, isPreset := ResolvePreset(bundle); isPreset {
			if preset == "" {
				preset = canon
			}
			bundle = ""
		} else {
			if !validName(bundle) {
				return cfg, fmt.Errorf("config bundle names use letters, digits, '-', '_' and '.' only")
			}
			bundleCfg, err = readJSONMap(BundlePath(project, bundle))
			if err != nil {
				return cfg, err
			}
			if bundleCfg == nil {
				names, _ := ListBundles(project)
				return cfg, fmt.Errorf("no config bundle %q in %s (have: %s; built-in presets: %s)", bundle, BundlesDir(project), strings.Join(names, ", "), strings.Join(PresetNames(), ", "))
			}
		}
	}
	// Preset precedence: flag > env > bundle > file.
	if preset == "" {
		preset = explicitPreset
	}
	if preset == "" {
		if raw, ok := bundleCfg["preset"]; ok {
			_ = json.Unmarshal(raw, &preset)
		}
	}
	if preset == "" {
		if raw, ok := fileCfg["preset"]; ok {
			_ = json.Unmarshal(raw, &preset)
		}
	}
	if preset != "" {
		canon, ok := ResolvePreset(preset)
		if ok {
			preset = canon
		}
		apply := Presets[canon]
		if !ok {
			return cfg, fmt.Errorf("unknown preset %q (have %s)", preset, strings.Join(PresetNames(), ", "))
		}
		apply(&cfg)
		cfg.Preset = preset
	}
	if err := overlay(&cfg, fileCfg, File(project)); err != nil {
		return cfg, err
	}
	if bundle != "" {
		if err := overlay(&cfg, bundleCfg, BundlePath(project, bundle)); err != nil {
			return cfg, err
		}
		cfg.Name = bundle
	}
	applyEnv(&cfg)
	normalize(&cfg)
	cfg.Instructions = loadInstructions(project)
	return cfg, cfg.Validate()
}

// normalize spells out what an empty setting means: an empty summarizer
// mode is inline.
func normalize(cfg *Config) {
	if cfg.Summarizer.Mode == "" {
		cfg.Summarizer.Mode = SummarizerInline
	}
}

func readJSONMap(path string) (map[string]json.RawMessage, error) {
	raw, err := boundedfile.Read(path, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// overlay merges a JSON object into cfg, carrying over any keys of the
// three-actor design first. Route objects merge field by field so a bundle
// can change just a model name.
func overlay(cfg *Config, m map[string]json.RawMessage, source string) error {
	if len(m) == 0 {
		return nil
	}
	m, notes := MigrateLegacy(m)
	for _, n := range notes {
		cfg.Migrated = append(cfg.Migrated, filepath.Base(source)+": "+n)
	}
	raw, _ := json.Marshal(m)
	if err := json.Unmarshal(raw, cfg); err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	return nil
}

// envFirst returns the first of the named variables that is set and not
// empty.
func envFirst(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// applyRouteEnv overrides a route from EAGENT_<prefix>* variables; legacy
// lists the prefixes the same settings had in the three-actor design, read
// when the current name is unset.
func applyRouteEnv(a *ModelRoute, prefix string, legacy ...string) {
	names := func(suffix string) []string {
		out := []string{"EAGENT_" + prefix + suffix}
		for _, l := range legacy {
			out = append(out, "EAGENT_"+l+suffix)
		}
		return out
	}
	if v := envFirst(names("MODEL")...); v != "" {
		a.Model = v
	}
	if v := envFirst(names("PROTOCOL")...); v != "" {
		a.Protocol = v
	}
	if v := envFirst(names("BASE_URL")...); v != "" {
		a.BaseURL = v
	}
	for _, n := range names("API_KEY_ENV") {
		if v, ok := os.LookupEnv(n); ok {
			a.APIKeyEnv = v
			break
		}
	}
	if v, err := strconv.Atoi(envFirst(names("MAX_TOKENS")...)); err == nil && v > 0 {
		a.MaxTokens = v
	}
	switch strings.ToLower(envFirst(names("REPLAY_REASONING")...)) {
	case "1", "true", "on", "yes":
		on := true
		a.ReplayReasoning = &on
	case "0", "false", "off", "no":
		off := false
		a.ReplayReasoning = &off
	}
}

func applyEnv(cfg *Config) {
	if value, err := strconv.ParseBool(os.Getenv("EAGENT_FINALECHAT_ARTIFACTS")); err == nil {
		cfg.Finalechat.Artifacts = value
	}
	// EAGENT_MODEL, EAGENT_PROTOCOL, EAGENT_BASE_URL, ... The orchestrator's
	// old names still name the model. The one model has one effort, and the
	// old names each set a different one, so neither is read for it:
	// EAGENT_REASONING_EFFORT sets it.
	applyRouteEnv(&cfg.Model, "", "ORCHESTRATOR_")
	if v := os.Getenv("EAGENT_REASONING_EFFORT"); v != "" {
		cfg.Model.ReasoningEffort = v
	}
	if v := os.Getenv("EAGENT_SUMMARIZER_MODE"); v != "" {
		cfg.Summarizer.Mode = v
	}
	if os.Getenv("EAGENT_SUMMARIZER_MODEL") != "" || cfg.Summarizer.Model != nil {
		route := cfg.Summarizer.Route(cfg.Model)
		route.Fallback = nil
		if cfg.Summarizer.Model != nil {
			route = *cfg.Summarizer.Model
		}
		applyRouteEnv(&route, "SUMMARIZER_")
		if v := os.Getenv("EAGENT_SUMMARIZER_REASONING_EFFORT"); v != "" {
			route.ReasoningEffort = v
		}
		cfg.Summarizer.Model = &route
	}
	if v, err := strconv.Atoi(os.Getenv("EAGENT_ROLLOVER_TOKENS")); err == nil && v > 0 {
		cfg.RolloverTokens = v
	}
	if v, err := strconv.Atoi(envFirst("EAGENT_QUIET_SECONDS", "EAGENT_NARRATOR_QUIET_SECONDS")); err == nil && v >= 0 {
		cfg.QuietSeconds = v
	}
	if v, err := strconv.Atoi(envFirst("EAGENT_TICK_SECONDS", "EAGENT_NARRATOR_TICK_SECONDS")); err == nil && v > 0 {
		cfg.TickSeconds = v
	}
	if v := os.Getenv("EAGENT_PERSONA"); v != "" {
		cfg.Persona = v
	}
	// (EAGENT_RELAY is taken: it is set inside an agent's terminal.)
	if v := os.Getenv("EAGENT_DEFAULT_RELAY"); v != "" {
		cfg.Relay.Default = v
	}
	switch strings.ToLower(os.Getenv("EAGENT_FINALECHAT")) {
	case "0", "off", "false", "no":
		off := false
		cfg.Finalechat.Enabled = &off
	case "1", "on", "true", "yes":
		on := true
		cfg.Finalechat.Enabled = &on
	}
}

// loadInstructions reads the project's notes for eagent itself. The agent
// eagent runs reads AGENTS.md on its own, so that file is not eagent's.
func loadInstructions(project string) string {
	name := filepath.Join(project, ".agents", "eagent", "INSTRUCTIONS.md")
	if raw, err := boundedfile.Read(name, 1<<20); err == nil && strings.TrimSpace(string(raw)) != "" {
		return strings.TrimSpace(string(raw))
	}
	return ""
}

// Validate checks structural sanity (not credentials).
func (c Config) Validate() error {
	var errs []string
	for _, p := range c.Problems() {
		if p.Severity == "error" {
			errs = append(errs, strings.TrimPrefix(p.Path, "/")+": "+p.Message)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "; "))
}

// Endpoint builds the wire endpoint, reading the key from the environment
// when APIKeyEnv is set (an empty APIKeyEnv means no key is needed).
func (a ModelRoute) Endpoint() (llm.Endpoint, error) {
	var key string
	if a.APIKeyEnv != "" {
		key = os.Getenv(a.APIKeyEnv)
		if key == "" {
			return llm.Endpoint{}, fmt.Errorf("%s is not set (needed for %s); export it, or pick another route with --preset or .agents/eagent/config.json", a.APIKeyEnv, a.Model)
		}
	}
	ep := llm.Endpoint{
		Protocol:        a.Protocol,
		BaseURL:         a.BaseURL,
		Model:           a.Model,
		APIKey:          key,
		ReasoningEffort: a.ReasoningEffort,
		ReplayReasoning: a.ReplayReasoning == nil || *a.ReplayReasoning,
		MaxTokens:       a.MaxTokens,
	}
	if strings.Contains(a.BaseURL, "openrouter.ai") {
		ep.Headers = map[string]string{
			"HTTP-Referer": "https://github.com/ericflo/eagent",
			"X-Title":      "eagent",
		}
	}
	return ep, nil
}

// Routes returns the primary endpoint followed by any fallbacks whose keys
// are present. An error is returned only if no route has a key.
func (a ModelRoute) Routes() ([]llm.Endpoint, error) {
	var eps []llm.Endpoint
	var errs []string
	for cur := &a; cur != nil; cur = cur.Fallback {
		ep, err := cur.Endpoint()
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		eps = append(eps, ep)
	}
	if len(eps) == 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return eps, nil
}

// Redacted returns a copy safe to print or log (keys are never stored, but
// this makes the intent explicit).
func (c Config) Redacted() Config { return c }

// Write saves the configuration file without overwriting an existing one.
func Write(project string, cfg Config) (string, error) {
	path := File(project)
	editor, err := LockEditor(context.Background(), project)
	if err != nil {
		return path, err
	}
	defer editor.Close()
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return path, err
	}
	if len(raw)+1 > 1<<20 {
		return path, fmt.Errorf("configuration exceeds 1 MiB")
	}
	return path, filelock.CreateFile(path, append(raw, '\n'), 0o644)
}
