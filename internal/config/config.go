// Package config resolves eagent's settings from defaults, an optional
// preset, the project's .agents/eagent/config.json, and EAGENT_* environment
// variables, in that order. Credentials are only ever read from the
// environment and are never written anywhere.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ericflo/eagent/internal/llm"
)

// Actor configures one model route.
type Actor struct {
	Protocol        string `json:"protocol"` // openai-chat | openai-responses | anthropic
	BaseURL         string `json:"base_url"`
	Model           string `json:"model"`
	APIKeyEnv       string `json:"api_key_env"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
	// ContextTokens is the model's usable window; the orchestrator rolls
	// over to a new subsession when its prompt approaches RolloverTokens.
	ContextTokens int `json:"context_tokens,omitempty"`
	// Fallback is tried when the primary route is unroutable (bad key, no
	// access to the model, exhausted credits).
	Fallback *Actor `json:"fallback,omitempty"`
}

// Config is the full effective configuration.
type Config struct {
	Preset       string `json:"preset,omitempty"`
	Orchestrator Actor  `json:"orchestrator"`
	Task         Actor  `json:"task"`
	Narrator     Actor  `json:"narrator"`

	// TaskConcurrency caps simultaneously running task-worker tasks.
	TaskConcurrency int `json:"task_concurrency"`
	// MaxTaskTurns bounds model calls per task before it is failed.
	MaxTaskTurns int `json:"max_task_turns"`
	// MaxOrchestratorCallsPerTurn bounds consecutive tool-calling rounds
	// before the harness inserts a checkpoint prompt.
	MaxOrchestratorCallsPerTurn int `json:"max_orchestrator_calls_per_turn"`
	// NarratorTickSeconds is how often the narrator is woken while the
	// orchestrator is busy and nothing else has woken it.
	NarratorTickSeconds int `json:"narrator_tick_seconds"`
	// RolloverTokens is the orchestrator prompt size that triggers a new
	// subsession and a dossier.
	RolloverTokens int `json:"rollover_tokens"`
	// BashWaitSeconds is how long `bash` waits inline for a command before
	// returning its handle.
	BashWaitSeconds int `json:"bash_wait_seconds"`
	// BashTimeoutSeconds is the default deadline for a command.
	BashTimeoutSeconds int `json:"bash_timeout_seconds"`
	// ToolOutputMaxChars truncates any single tool result.
	ToolOutputMaxChars int `json:"tool_output_max_chars"`
	// Persona overrides the narrator's voice.
	Persona string `json:"persona,omitempty"`
	// AllowOutsideProject lets file tools touch paths outside the project.
	AllowOutsideProject bool `json:"allow_outside_project"`
	// Instructions is loaded from AGENTS.md / .agents/eagent/INSTRUCTIONS.md.
	Instructions string `json:"-"`
}

const (
	togetherURL   = "https://api.together.xyz/v1"
	openaiURL     = "https://api.openai.com/v1"
	openrouterURL = "https://openrouter.ai/api/v1"
	anthropicURL  = "https://api.anthropic.com/v1"
)

// Defaults is the shipped configuration: GLM-5.3 orchestrating, GLM-5.3-Flash
// doing the work, DeepSeek V4 Flash narrating, all on Together AI.
func Defaults() Config {
	return Config{
		Orchestrator: Actor{
			Protocol: llm.ProtocolChat, BaseURL: togetherURL, Model: "zai-org/GLM-5.3",
			APIKeyEnv: "TOGETHER_API_KEY", ReasoningEffort: "medium", MaxTokens: 32768, ContextTokens: 200_000,
		},
		Task: Actor{
			Protocol: llm.ProtocolChat, BaseURL: togetherURL, Model: "zai-org/GLM-5.3-Flash",
			APIKeyEnv: "TOGETHER_API_KEY", ReasoningEffort: "low", MaxTokens: 32768, ContextTokens: 200_000,
		},
		Narrator: Actor{
			Protocol: llm.ProtocolChat, BaseURL: togetherURL, Model: "deepseek-ai/DeepSeek-V4-Flash-0731",
			APIKeyEnv: "TOGETHER_API_KEY", ReasoningEffort: "none", MaxTokens: 4096, ContextTokens: 200_000,
		},
		TaskConcurrency:             3,
		MaxTaskTurns:                100,
		MaxOrchestratorCallsPerTurn: 40,
		NarratorTickSeconds:         90,
		RolloverTokens:              150_000,
		BashWaitSeconds:             20,
		BashTimeoutSeconds:          600,
		ToolOutputMaxChars:          24_000,
	}
}

// Presets are named alternative routings.
var Presets = map[string]func(*Config){
	"glm": func(c *Config) {}, // the defaults
	"astra": func(c *Config) {
		c.Orchestrator = Actor{
			Protocol: llm.ProtocolResponses, BaseURL: openaiURL, Model: "gpt-6-astra",
			APIKeyEnv: "OPENAI_API_KEY", ReasoningEffort: "medium", MaxTokens: 32768, ContextTokens: 1_000_000,
			Fallback: &Actor{
				Protocol: llm.ProtocolResponses, BaseURL: openrouterURL, Model: "openai/gpt-6-astra",
				APIKeyEnv: "OPENROUTER_API_KEY", ReasoningEffort: "medium", MaxTokens: 32768, ContextTokens: 1_000_000,
			},
		}
		c.RolloverTokens = 300_000
	},
	"anthropic": func(c *Config) {
		c.Orchestrator = Actor{
			Protocol: llm.ProtocolAnthropic, BaseURL: anthropicURL, Model: "claude-opus-5",
			APIKeyEnv: "ANTHROPIC_API_KEY", ReasoningEffort: "medium", MaxTokens: 32768, ContextTokens: 200_000,
		}
		c.Task = Actor{
			Protocol: llm.ProtocolAnthropic, BaseURL: anthropicURL, Model: "claude-sonnet-5",
			APIKeyEnv: "ANTHROPIC_API_KEY", ReasoningEffort: "low", MaxTokens: 32768, ContextTokens: 200_000,
		}
		c.Narrator = Actor{
			Protocol: llm.ProtocolAnthropic, BaseURL: anthropicURL, Model: "claude-haiku-4-5-20251001",
			APIKeyEnv: "ANTHROPIC_API_KEY", MaxTokens: 4096, ContextTokens: 200_000,
		}
	},
}

// PresetNames lists presets in a stable order.
func PresetNames() []string { return []string{"glm", "astra", "anthropic"} }

// File is the per-project override path.
func File(project string) string { return filepath.Join(project, ".agents", "eagent", "config.json") }

// Load resolves the configuration for a project directory.
func Load(project string, preset string) (Config, error) {
	cfg := Defaults()
	fileCfg := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(File(project)); err == nil {
		if err := json.Unmarshal(raw, &fileCfg); err != nil {
			return cfg, fmt.Errorf("%s: %w", File(project), err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return cfg, err
	}
	// Preset precedence: flag > env > file.
	if preset == "" {
		preset = os.Getenv("EAGENT_PRESET")
	}
	if preset == "" {
		if raw, ok := fileCfg["preset"]; ok {
			_ = json.Unmarshal(raw, &preset)
		}
	}
	if preset != "" {
		apply, ok := Presets[preset]
		if !ok {
			return cfg, fmt.Errorf("unknown preset %q (have %s)", preset, strings.Join(PresetNames(), ", "))
		}
		apply(&cfg)
		cfg.Preset = preset
	}
	if len(fileCfg) > 0 {
		raw, _ := json.Marshal(fileCfg)
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("%s: %w", File(project), err)
		}
	}
	applyEnv(&cfg)
	cfg.Instructions = loadInstructions(project)
	return cfg, cfg.Validate()
}

func applyEnv(cfg *Config) {
	actors := map[string]*Actor{"ORCHESTRATOR": &cfg.Orchestrator, "TASK": &cfg.Task, "NARRATOR": &cfg.Narrator}
	for name, a := range actors {
		if v := os.Getenv("EAGENT_" + name + "_MODEL"); v != "" {
			a.Model = v
		}
		if v := os.Getenv("EAGENT_" + name + "_PROTOCOL"); v != "" {
			a.Protocol = v
		}
		if v := os.Getenv("EAGENT_" + name + "_BASE_URL"); v != "" {
			a.BaseURL = v
		}
		if v := os.Getenv("EAGENT_" + name + "_API_KEY_ENV"); v != "" {
			a.APIKeyEnv = v
		}
		if v := os.Getenv("EAGENT_" + name + "_REASONING_EFFORT"); v != "" {
			a.ReasoningEffort = v
		}
		if v, err := strconv.Atoi(os.Getenv("EAGENT_" + name + "_MAX_TOKENS")); err == nil && v > 0 {
			a.MaxTokens = v
		}
	}
	if v, err := strconv.Atoi(os.Getenv("EAGENT_TASK_CONCURRENCY")); err == nil && v > 0 {
		cfg.TaskConcurrency = v
	}
	if v, err := strconv.Atoi(os.Getenv("EAGENT_ROLLOVER_TOKENS")); err == nil && v > 0 {
		cfg.RolloverTokens = v
	}
	if v, err := strconv.Atoi(os.Getenv("EAGENT_NARRATOR_TICK_SECONDS")); err == nil && v > 0 {
		cfg.NarratorTickSeconds = v
	}
	if v := os.Getenv("EAGENT_PERSONA"); v != "" {
		cfg.Persona = v
	}
}

func loadInstructions(project string) string {
	for _, name := range []string{
		filepath.Join(project, ".agents", "eagent", "INSTRUCTIONS.md"),
		filepath.Join(project, "AGENTS.md"),
	} {
		if raw, err := os.ReadFile(name); err == nil && strings.TrimSpace(string(raw)) != "" {
			return strings.TrimSpace(string(raw))
		}
	}
	return ""
}

// Validate checks structural sanity (not credentials).
func (c Config) Validate() error {
	for name, a := range map[string]Actor{"orchestrator": c.Orchestrator, "task": c.Task, "narrator": c.Narrator} {
		if err := a.validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if c.TaskConcurrency < 1 {
		return errors.New("task_concurrency must be >= 1")
	}
	if c.RolloverTokens < 20_000 {
		return errors.New("rollover_tokens must be >= 20000")
	}
	return nil
}

func (a Actor) validate() error {
	switch a.Protocol {
	case llm.ProtocolChat, llm.ProtocolResponses, llm.ProtocolAnthropic:
	default:
		return fmt.Errorf("unknown protocol %q", a.Protocol)
	}
	if a.Model == "" {
		return errors.New("model is required")
	}
	if a.BaseURL == "" {
		return errors.New("base_url is required")
	}
	if a.APIKeyEnv == "" {
		return errors.New("api_key_env is required")
	}
	if a.Fallback != nil {
		if err := a.Fallback.validate(); err != nil {
			return fmt.Errorf("fallback: %w", err)
		}
	}
	return nil
}

// Endpoint builds the wire endpoint, reading the key from the environment.
func (a Actor) Endpoint() (llm.Endpoint, error) {
	key := os.Getenv(a.APIKeyEnv)
	if key == "" {
		return llm.Endpoint{}, fmt.Errorf("%s is not set (needed for %s)", a.APIKeyEnv, a.Model)
	}
	ep := llm.Endpoint{
		Protocol:        a.Protocol,
		BaseURL:         a.BaseURL,
		Model:           a.Model,
		APIKey:          key,
		ReasoningEffort: a.ReasoningEffort,
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
func (a Actor) Routes() ([]llm.Endpoint, error) {
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
	if _, err := os.Stat(path); err == nil {
		return path, fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return path, err
	}
	return path, os.WriteFile(path, append(raw, '\n'), 0o644)
}
