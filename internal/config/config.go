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
	// ContextTokens is the model's usable window. The conversation restarts
	// from a dossier before it fills: by default at half of it (Rollover).
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
	// restarted from a dossier in a new subsession. 0, the default, follows
	// the model in use: half its context window (AutoRollover). A number
	// fixes it whatever the model.
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
	// Instructions is the project's instructions for the agent's work, read
	// from .agents/eagent/INSTRUCTIONS.md or, when that is missing or empty,
	// the project's AGENTS.md. eagent is shown them so it can pass them on
	// when they are relevant.
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
	// false (or EAGENT_FINALECHAT_ARTIFACTS=0) to opt out. Always written:
	// false is the opt-out, and a file that left it out would read as on.
	// A file without the key means on, as it always has.
	Artifacts bool `json:"artifacts"`
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

// loopEffort is the effort the presets run at: the one the orchestrator of
// the three-actor design ran at. The one model makes the same decisions (a
// stuck terminal, autopilot, what to tell the user), and it has one effort
// for every call of a session, because many engines put the effort into
// the prompt and a change of effort would cost the provider's cache of the
// conversation. model.reasoning_effort sets another level.
const loopEffort = "xhigh"

// presetEffort is loopEffort where the model offers it, and otherwise the
// highest effort it offers below max: high for GLM, Kimi, Mistral and
// DeepSeek. Never none, never max.
func presetEffort(baseURL, model string) string {
	m, ok := Lookup(baseURL, model)
	if !ok || len(m.Efforts) == 0 || contains(m.Efforts, loopEffort) {
		return loopEffort
	}
	best := ""
	for _, e := range m.Efforts {
		if e == "none" || e == "max" {
			continue
		}
		if best == "" || EffortIndex(e) > EffortIndex(best) {
			best = e
		}
	}
	if best == "" {
		return loopEffort
	}
	return best
}

// AutoRollover is the prompt size at which a conversation restarts from a
// dossier when rollover_tokens is not set: half the context window of the
// model in use, in whole thousands. Half is what the conversation may fill.
// The rest is the room a call needs for its reply and the dossier pass
// needs to read the record it maps, on top of the conversation it is
// written in. A small window with a large reply ceiling gets less than
// half, so that pass still fits; nothing gets under 20,000.
func AutoRollover(window, maxTokens int) int {
	if window <= 0 {
		window = 200_000
	}
	n := window / 2
	if room := window - maxTokens - rolloverReserve; n > room {
		n = room
	}
	return max(n/1000*1000, 20_000)
}

// AutoRolloverWithin is AutoRollover held inside what the route can take
// as input: its window less the reply ceiling (or the 16,384 tokens a call
// asks for when the route sets none), of which fixed tokens are the system
// prompt and the tool list that open every request. A conversation may
// fill three quarters of what is left beside them, which leaves the rest
// for the turn that carries it past the line, and is given at least a
// quarter: AutoRollover's 20,000 floor knows nothing of the fixed part,
// and on a window whose reply ceiling eats its reserve (64k to 96k with a
// large ceiling) it can sit at or below what a long INSTRUCTIONS.md and
// the tools take, where no restart could make room. On a window where
// AutoRollover's number already fits (every ordinary cloud model) the
// number is AutoRollover's. A window too small to hold the fixed part at
// all is left at AutoRollover's: no restart can make room there.
func AutoRolloverWithin(window, maxTokens, fixed int) int {
	n := AutoRollover(window, maxTokens)
	if window <= 0 {
		window = 200_000
	}
	if maxTokens <= 0 {
		maxTokens = 16384
	}
	room := window - maxTokens - max(fixed, 0)
	if room <= 0 {
		return n
	}
	// In hundreds: on a window this small a thousand is a good part of
	// the room the conversation has.
	limit := max(fixed, 0) + room*3/4
	if limit/100*100 > max(fixed, 0) {
		limit = limit / 100 * 100
	}
	least := max(fixed, 0) + room/4
	return min(max(n, least), limit)
}

// rolloverReserve is what a restart needs beside the conversation and the
// reply: 8,000 tokens of slack and the least the dossier pass reads of the
// record (60,000 characters, about 25,000 tokens with a fifth held back).
const rolloverReserve = 8_000 + 25_000

// Rollover is the prompt size at which this configuration's conversation
// restarts: the number it sets, or AutoRollover for its model.
func (c Config) Rollover() int {
	if c.RolloverTokens > 0 {
		return c.RolloverTokens
	}
	// The system prompt is not known here; a running session takes it off
	// too, so on a small window it restarts a little later than this says.
	return AutoRolloverWithin(c.Model.ContextTokens, c.Model.MaxTokens, 0)
}

// glmDescription describes the built-in defaults, which the "glm" preset names.
const glmDescription = "GLM-5.3 (Together AI)"

// Defaults is the shipped configuration: GLM-5.3 on Together AI at the
// effort its orchestrator ran at (high, the most GLM offers), with the
// thread's title and summary kept inline.
func Defaults() Config {
	return Config{
		Model:           chatRoute(togetherURL, "TOGETHER_API_KEY", "zai-org/GLM-5.3", 200_000),
		Summarizer:      Summarizer{Mode: SummarizerInline},
		MaxCallsPerTurn: 30,
		TickSeconds:     30,
		QuietSeconds:    180,
		ReportMaxChars:  20000,
		Finalechat:      Finalechat{Artifacts: true},
	}
}

// Presets are named models to run eagent on. The provider-tier names
// (openai-high, openrouter-med, anthropic-med, ...) trade cost for capability
// within one provider; glm is the default.
//
// Each preset runs the model its orchestrator ran in the three-actor design,
// on the same route (presets_test.go holds the list).
//
// Every preset runs at one effort for every call, the one its orchestrator
// ran at: xhigh, or the highest the model offers below max (see loopEffort
// and presetEffort).
var Presets = map[string]func(*Config){
	"glm": func(c *Config) {}, // the defaults
	"openai-high": func(c *Config) {
		c.Description = "GPT-6 Astra (OpenAI, OpenRouter fallback)"
		c.Model = openaiRoute("gpt-6-astra")
	},
	"openai-low": func(c *Config) {
		c.Description = "GPT-6.1 Sol (OpenAI, OpenRouter fallback)"
		c.Model = openaiRoute("gpt-6.1-sol")
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
var presetAliases = map[string]string{
	"anthropic":  "anthropic-high",
	"astra":      "openai-high",
	"openai-med": "openai-high",
}

// mergedPresets are the names that were presets of their own while a
// preset set a model per actor. They ran the same orchestrator as the
// preset they now name, and differed only in the task worker's model, so
// with one model they are that preset. A config that names one is told
// (PresetNote).
var mergedPresets = map[string]bool{"astra": true, "openai-med": true}

// PresetNote says what became of a preset name that is now another's, or
// "" for any other name.
func PresetNote(name string) string {
	if !mergedPresets[name] {
		return ""
	}
	canon := presetAliases[name]
	return fmt.Sprintf("preset %q is now %q: both ran %s, and differed only in the task worker's model, which is gone", name, canon, PresetDescription(canon))
}

// PresetNames lists presets in a stable order.
func PresetNames() []string {
	return []string{
		"glm",
		"openai-high", "openai-low",
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
		APIKeyEnv: keyEnv, ReasoningEffort: presetEffort(baseURL, model), MaxTokens: loopMaxTokens, ContextTokens: contextTokens,
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
	if why := KeptBy(Resolve(project, "", ""), "", "", name); len(why) > 0 {
		return fmt.Errorf("%q is in force: %s", name, strings.Join(why, "; "))
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
	// A saved preset loads over the project file and the preset that file
	// names, so every value it leaves empty is spelled out (spelled): a
	// missing fallback or effort would otherwise be the file's or preset's.
	pretty, err := json.MarshalIndent(spelled(cfg), "", "  ")
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
// DefaultBundle is the bundle loading takes when none is named and no
// explicit preset names a saved one: EAGENT_CONFIG, else the file's
// default_config. It may name a built-in preset, which is then the preset.
func DefaultBundle(fileCfg map[string]json.RawMessage) string {
	if bundle := os.Getenv("EAGENT_CONFIG"); bundle != "" {
		return bundle
	}
	var bundle string
	if raw, ok := fileCfg["default_config"]; ok {
		_ = json.Unmarshal(raw, &bundle)
	}
	return bundle
}

// explicitChoice is the one place an explicit preset (the caller's, else
// EAGENT_PRESET) is taken as loading takes it: a saved preset it names is
// the bundle, unless the caller names a bundle, which outranks the
// variable; anything else it names stays a preset. It returns the preset
// and bundle to lay, and the explicit preset still to apply.
func explicitChoice(project, preset, bundle string) (string, string, string) {
	explicit, fromEnv := preset, preset == ""
	if fromEnv {
		explicit = os.Getenv("EAGENT_PRESET")
	}
	if _, isBuiltIn := ResolvePreset(explicit); explicit == "" || isBuiltIn || !validName(explicit) {
		return preset, bundle, explicit
	}
	if _, err := os.Stat(BundlePath(project, explicit)); err != nil {
		return preset, bundle, explicit
	}
	switch {
	case bundle == "":
		return "", explicit, ""
	case fromEnv:
		return preset, bundle, "" // the bundle named outranks the variable
	}
	return preset, bundle, explicit
}

// IsDefaultBundle says whether the saved bundle name is in force, so it is
// not to be deleted (KeptBy).
func IsDefaultBundle(res Resolution, name string) bool {
	return len(KeptBy(res, "", "", name)) > 0
}

// FlagKeeps is why a saved preset eagent was started on (--config, or
// --preset naming it) is in force, and what frees it.
const FlagKeeps = "this eagent was started on it (--config or --preset); it can be deleted once eagent runs on another"

// KeptBy says what keeps the saved bundle name in force, so it is not to be
// deleted, each with what would free it; none when nothing does. res is
// Resolve's for the caller's preset and bundle. The bundle loading lays is
// kept: the one res laid (the caller's, else a saved preset its preset or
// EAGENT_PRESET names: explicitChoice), else EAGENT_CONFIG's; and so is the
// file's default_config, which every start naming none takes.
func KeptBy(res Resolution, preset, bundle, name string) []string {
	if name == "" {
		return nil
	}
	var why []string
	if res.Effective.DefaultConfig == name {
		why = append(why, "it is the project's default (default_config); select a different default first")
	}
	switch {
	case res.Active.Kind == "bundle" && res.Active.Name == name && (preset != "" || bundle != ""):
		why = append(why, FlagKeeps)
	case res.Active.Kind == "bundle" && res.Active.Name == name:
		why = append(why, "EAGENT_PRESET names it in eagent's environment; it can be deleted once that variable names another")
	case res.Active.Kind != "bundle" && os.Getenv("EAGENT_CONFIG") == name:
		why = append(why, "EAGENT_CONFIG names it in eagent's environment; it can be deleted once that variable names another")
	}
	return why
}

func LoadBundleWithFile(project, preset, bundle string, fileCfg map[string]json.RawMessage) (Config, error) {
	cfg := Defaults()
	var err error
	preset, bundle, explicitPreset := explicitChoice(project, preset, bundle)
	if bundle == "" {
		bundle = DefaultBundle(fileCfg)
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
		if note := PresetNote(preset); note != "" {
			cfg.Migrated = append(cfg.Migrated, note)
		}
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
	var n named
	if err := overlay(project, &cfg, fileCfg, File(project), &n); err != nil {
		return cfg, err
	}
	cfg.Name = "" // the project file is no bundle, whatever "name" it carries
	if bundle != "" {
		if err := overlay(project, &cfg, bundleCfg, BundlePath(project, bundle), &n); err != nil {
			return cfg, err
		}
		cfg.Name = bundle
	}
	applyEnv(project, &cfg, n)
	normalize(&cfg)
	cfg.Instructions = loadInstructions(project)
	return cfg, cfg.Validate()
}

// ClearNulls empties, in each route of the main and summarizer chains, a
// context window, effort or reply ceiling that m gives as null: what the
// editor writes for a field it leaves empty. It is for a whole
// configuration decoded onto the defaults, where nothing lies below to
// clear; a layer over a preset is overlay's (followModel).
func ClearNulls(cfg *Config, m map[string]json.RawMessage) {
	clear := func(a *ModelRoute, layer json.RawMessage) {
		obj := object(layer)
		for cur := a; cur != nil && obj != nil; cur = cur.Fallback {
			if string(obj["context_tokens"]) == "null" {
				cur.ContextTokens = 0
			}
			if string(obj["reasoning_effort"]) == "null" {
				cur.ReasoningEffort = ""
			}
			if string(obj["max_tokens"]) == "null" {
				cur.MaxTokens = 0
			}
			obj = object(obj["fallback"])
		}
	}
	clear(&cfg.Model, m["model"])
	clear(cfg.Summarizer.Model, object(m["summarizer"])["model"])
}

// ChosenPreset is a built-in preset as a running session gets it when the
// user picks it from the list of configurations: the project file's
// settings over the preset, as at a start with --preset, except which model
// the file names and where it is reached (and its default_config). The
// model is what picking a preset chooses; the file's own would make every
// preset the same model, or a broken one (a model name over another
// provider's address). The file's effort and reply ceiling still apply.
func ChosenPreset(project, preset string) (Config, error) {
	file, err := readJSONMap(File(project))
	if err != nil {
		return Defaults(), err
	}
	return ChosenPresetWithFile(project, preset, file)
}

// ChosenPresetWithFile is ChosenPreset over given file contents.
func ChosenPresetWithFile(project, preset string, file map[string]json.RawMessage) (Config, error) {
	if _, ok := ResolvePreset(preset); !ok {
		return Defaults(), fmt.Errorf("unknown preset %q (have %s)", preset, strings.Join(PresetNames(), ", "))
	}
	rest, _ := DropModelChoice(file)
	delete(rest, "default_config")
	delete(rest, "preset")
	// The preset named as the bundle too: EAGENT_CONFIG does not replace it.
	// A route the environment pins (EAGENT_MODEL and the like) still wins,
	// as it does everywhere; a choice that cannot hold under it is refused.
	cfg, err := LoadBundleWithFile(project, preset, preset, rest)
	if err == nil {
		OfferedEffort(project, &cfg)
	}
	return cfg, err
}

// OfferedEffort moves an effort kept that a route's model does not offer
// (xhigh from a file written for openai-high, on GLM-5.3) to the nearest
// one it does, as a live switch to that model makes it (PickRoute, with
// the project's servers and lists): on the main route, and on a dedicated
// summarizer's own route.
func OfferedEffort(project string, cfg *Config) {
	routes := []*ModelRoute{&cfg.Model}
	if cfg.Summarizer.Mode == SummarizerDedicated && cfg.Summarizer.Model != nil && cfg.Summarizer.Model.Model != "" {
		routes = append(routes, cfg.Summarizer.Model)
	}
	for _, a := range routes {
		if pr, ok := ProviderForURLFor(project, a.BaseURL); ok {
			if picked, err := PickRoute(project, ModelRoute{ReasoningEffort: a.ReasoningEffort}, pr.ID, a.Model, ""); err == nil {
				a.ReasoningEffort = picked.ReasoningEffort
			}
		}
	}
}

// modelChoiceFields are the fields of a route that say which model it is
// and where it is reached: what choosing a preset chooses.
var modelChoiceFields = []string{"protocol", "base_url", "model", "api_key_env", "context_tokens", "fallback"}

// DropModelChoice returns a configuration object, carried over to today's
// keys, without the model its route names or where it is reached, so a
// preset under it decides them; the route's other settings (its effort,
// its reply ceiling) stay. dropped is the model it named, "" when none.
func DropModelChoice(file map[string]json.RawMessage) (out map[string]json.RawMessage, dropped string) {
	migrated, _ := MigrateLegacy(file)
	out = make(map[string]json.RawMessage, len(migrated))
	for k, v := range migrated {
		out[k] = v
	}
	route := object(out["model"])
	if route == nil {
		return out, ""
	}
	_ = json.Unmarshal(route["model"], &dropped)
	for _, k := range modelChoiceFields {
		delete(route, k)
	}
	if len(route) == 0 {
		delete(out, "model")
	} else {
		out["model"], _ = json.Marshal(route)
	}
	return out, dropped
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

// named says, of the main route and of the summarizer's own, whether the
// window it has is one a layer names for it: a variable that moves the
// route's address leaves that window alone (applyEnv).
type named struct{ main, summarizer bool }

// overlay merges a JSON object into cfg, carrying over any keys of the
// three-actor design first. Route objects merge field by field so a bundle
// can change just a model name; a route whose model the object changes
// without naming its context window gets the new model's (followModel).
// names says afterwards whose windows a layer names.
func overlay(project string, cfg *Config, m map[string]json.RawMessage, source string, names *named) error {
	if len(m) == 0 {
		return nil
	}
	m, notes := MigrateLegacy(m)
	for _, n := range notes {
		cfg.Migrated = append(cfg.Migrated, filepath.Base(source)+": "+n)
	}
	mainBefore, summarizerBefore := chainRoutes(&cfg.Model), chainRoutes(cfg.Summarizer.Model)
	raw, _ := json.Marshal(m)
	if err := json.Unmarshal(raw, cfg); err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	names.main = followModel(project, &cfg.Model, mainBefore, m["model"], names.main)
	names.summarizer = followModel(project, cfg.Summarizer.Model, summarizerBefore, object(m["summarizer"])["model"], names.summarizer)
	return nil
}

// Layer lays a configuration object over cfg as the loader lays the file
// or a bundle (overlay): a route it moves to another model or address
// without naming a window gets the window there, and a null clears what
// lies below (followModel).
func Layer(project string, cfg *Config, m map[string]json.RawMessage, source string) error {
	return overlay(project, cfg, m, source, &named{})
}

// chainRoutes copies a route and each of its fallbacks.
func chainRoutes(a *ModelRoute) []ModelRoute {
	var out []ModelRoute
	for cur := a; cur != nil; cur = cur.Fallback {
		out = append(out, *cur)
	}
	return out
}

// moved says whether a route names another model than it did before, or
// reaches it at another address (with or without a trailing slash, the
// same address): its window is no longer the one it had.
func moved(before, now ModelRoute) bool {
	return now.Model != before.Model || strings.TrimRight(now.BaseURL, "/") != strings.TrimRight(before.BaseURL, "/")
}

// followModel gives each route of a chain whose model or address changed
// from before the window of the model where it is now reached (0 when it
// is not known), unless layer, the route object that changed it, names a
// context window itself. A window is the model's at its server: one kept
// from the model before, or from another server, would be wrong.
//
// A null in layer clears what the layers below set: the editor writes one
// where it leaves a field empty that the preset sets, such as an effort of
// "" (send none, the provider's default) under a preset's "high".
//
// It returns whether the route's own window is now one a layer names: yes
// when layer names one, no when layer clears it or moves the route without
// naming one, else named, as the layers below left it.
func followModel(project string, a *ModelRoute, before []ModelRoute, layer json.RawMessage, named bool) bool {
	if a == nil {
		return false
	}
	obj := object(layer)
	null := func(k string) bool { v, ok := obj[k]; return ok && string(v) == "null" }
	for i, cur := 0, a; cur != nil; i, cur = i+1, cur.Fallback {
		var prev ModelRoute
		if i < len(before) {
			prev = before[i]
		}
		_, window := obj["context_tokens"]
		if i == 0 {
			named = window && !null("context_tokens") || !window && named && !moved(prev, *cur)
		}
		switch {
		case moved(prev, *cur) && (!window || null("context_tokens")):
			cur.ContextTokens = KnownContext(project, cur.BaseURL, cur.Model)
		case null("context_tokens"):
			cur.ContextTokens = 0
		}
		if null("reasoning_effort") {
			cur.ReasoningEffort = ""
		}
		if null("max_tokens") {
			cur.MaxTokens = 0
		}
		obj = object(obj["fallback"])
	}
	return named
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

// summarizerEnv names the first EAGENT_SUMMARIZER_* variable that sets a
// field of the summarizer's route, or "" when none does.
func summarizerEnv() string {
	for _, suffix := range []string{"MODEL", "PROTOCOL", "BASE_URL", "API_KEY_ENV", "REASONING_EFFORT", "MAX_TOKENS", "REPLAY_REASONING"} {
		name := "EAGENT_SUMMARIZER_" + suffix
		if _, ok := os.LookupEnv(name); (ok && suffix == "API_KEY_ENV") || os.Getenv(name) != "" {
			return name
		}
	}
	return ""
}

func applyEnv(project string, cfg *Config, n named) {
	if value, err := strconv.ParseBool(os.Getenv("EAGENT_FINALECHAT_ARTIFACTS")); err == nil {
		cfg.Finalechat.Artifacts = value
	}
	// EAGENT_MODEL, EAGENT_PROTOCOL, EAGENT_BASE_URL, ... The orchestrator's
	// old names still name the model when the new ones are unset, and so
	// does its effort: the one model does the orchestrator's work. The
	// narrator's and the task worker's variables are not read; each one set
	// gets a note. A variable that names another model gives the route that
	// model's window; one that moves it to another address, that server's,
	// unless a layer names the window for the route (n).
	prev := cfg.Model
	applyRouteEnv(&cfg.Model, "", "ORCHESTRATOR_")
	if cfg.Model.Model != prev.Model || !n.main && moved(prev, cfg.Model) {
		cfg.Model.ContextTokens = KnownContext(project, cfg.Model.BaseURL, cfg.Model.Model)
		n.main = false
	}
	if v := envFirst("EAGENT_REASONING_EFFORT", "EAGENT_ORCHESTRATOR_REASONING_EFFORT"); v != "" {
		cfg.Model.ReasoningEffort = v
	}
	cfg.Migrated = append(cfg.Migrated, retiredEnvNotes()...)
	if v := os.Getenv("EAGENT_SUMMARIZER_MODE"); v != "" {
		cfg.Summarizer.Mode = v
	}
	// Any EAGENT_SUMMARIZER_* route variable gives the summarizer a route of
	// its own: the one written, else a copy of the main model's to change.
	if summarizerEnv() != "" || cfg.Summarizer.Model != nil {
		route := cfg.Summarizer.Route(cfg.Model)
		route.Fallback = nil
		own := n.main
		if cfg.Summarizer.Model != nil {
			route = *cfg.Summarizer.Model
			own = n.summarizer
		}
		prev := route
		applyRouteEnv(&route, "SUMMARIZER_")
		if route.Model != prev.Model || !own && moved(prev, route) {
			route.ContextTokens = KnownContext(project, route.BaseURL, route.Model)
		}
		if v := os.Getenv("EAGENT_SUMMARIZER_REASONING_EFFORT"); v != "" {
			route.ReasoningEffort = v
		}
		cfg.Summarizer.Model = &route
	}
	// 0 is a value of its own: follow the model in use, over a number the
	// file fixes.
	if v, err := strconv.Atoi(os.Getenv("EAGENT_ROLLOVER_TOKENS")); err == nil && v >= 0 {
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

// retiredEnvNotes names each variable of the three-actor design that is set
// and no longer has any effect. A variable's value is never part of a note.
func retiredEnvNotes() []string {
	var notes []string
	for _, actor := range []struct{ prefix, why string }{
		{"EAGENT_NARRATOR_", "one model does every call now"},
		{"EAGENT_TASK_", "there are no task workers now"},
	} {
		for _, suffix := range []string{"MODEL", "PROTOCOL", "BASE_URL", "API_KEY_ENV", "REASONING_EFFORT", "MAX_TOKENS", "REPLAY_REASONING"} {
			if envSet(actor.prefix + suffix) {
				notes = append(notes, actor.prefix+suffix+" is set but no longer read: "+actor.why)
			}
		}
	}
	if envSet("EAGENT_TASK_CONCURRENCY") {
		notes = append(notes, "EAGENT_TASK_CONCURRENCY is set but no longer read: there are no task workers now")
	}
	return notes
}

// envSet reports whether a variable would have had an effect in the
// three-actor design: set and not empty, or set at all for a key variable
// (empty meant no key is needed).
func envSet(name string) bool {
	v, ok := os.LookupEnv(name)
	if strings.HasSuffix(name, "_API_KEY_ENV") {
		return ok
	}
	return v != ""
}

// loadInstructions reads the project's instructions for the agent's work:
// .agents/eagent/INSTRUCTIONS.md, or the project's AGENTS.md when that is
// missing or empty.
func loadInstructions(project string) string {
	for _, name := range []string{
		filepath.Join(project, ".agents", "eagent", "INSTRUCTIONS.md"),
		filepath.Join(project, "AGENTS.md"),
	} {
		if raw, err := boundedfile.Read(name, 1<<20); err == nil && strings.TrimSpace(string(raw)) != "" {
			return strings.TrimSpace(string(raw))
		}
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
	// The file builds on the preset it names, so what cfg leaves empty is
	// spelled out to stay empty.
	raw, err := marshalOrdered(spelled(cfg))
	if err != nil {
		return path, err
	}
	if len(raw) > 1<<20 {
		return path, fmt.Errorf("configuration exceeds 1 MiB")
	}
	return path, filelock.CreateFile(path, raw, 0o644)
}
