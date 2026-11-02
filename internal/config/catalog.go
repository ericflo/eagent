package config

import (
	"sort"
	"strings"

	"github.com/ericflo/eagent/internal/llm"
)

// The catalog is the one place that knows which providers exist, which
// models they serve under which ids, what those cost, what context they
// carry, and which reasoning efforts each provider is known to accept for
// each model. Presets, the web configuration editor, and session cost
// estimates all read it, and tests keep the presets consistent with it.
//
// Everything here was checked against provider catalogs and live calls on
// 2026-09-07 (and, for the entries added later, against OpenRouter's live
// /api/v1/models on 2026-11-01); AsOf says so on each price, and Nominal
// marks a price copied from an upstream list rather than read from the
// provider itself.

// Provider is one API host.
type Provider struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	BaseURL  string `json:"base_url"`
	Protocol string `json:"protocol"` // default for its models; a model may override
	KeyEnv   string `json:"key_env"`
	Note     string `json:"note,omitempty"`
}

// Price is USD per million tokens.
type Price struct {
	In      float64 `json:"in"`
	Cached  float64 `json:"cached"`
	Out     float64 `json:"out"`
	AsOf    string  `json:"as_of"`
	Source  string  `json:"source"`
	Nominal bool    `json:"nominal,omitempty"` // copied from an upstream list, not the provider's own page
}

// Model is one model at one provider.
type Model struct {
	Provider string   `json:"provider"`
	ID       string   `json:"id"`
	Family   string   `json:"family"` // the same model across providers shares a family
	Label    string   `json:"label"`
	Protocol string   `json:"protocol,omitempty"` // when it differs from the provider's default
	Price    *Price   `json:"price,omitempty"`
	Context  int      `json:"context"`
	Vision   bool     `json:"vision"`
	Efforts  []string `json:"efforts,omitempty"` // reasoning efforts known to work here, in order; nil means unchecked
	Note     string   `json:"note,omitempty"`
	// Live marks models confirmed present by a provider's /models endpoint at
	// the last live fetch (see live.go); the static catalog never sets it.
	Live bool `json:"live,omitempty"`
}

// EffortOrder is the canonical order of effort values across providers.
var EffortOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// Providers, in display order.
var Providers = []Provider{
	{ID: "together", Label: "Together AI", BaseURL: togetherURL, Protocol: llm.ProtocolChat, KeyEnv: "TOGETHER_API_KEY"},
	{ID: "openrouter", Label: "OpenRouter", BaseURL: openrouterURL, Protocol: llm.ProtocolChat, KeyEnv: "OPENROUTER_API_KEY", Note: "one key, many labs; the account's data-policy setting can hide some models"},
	{ID: "openai", Label: "OpenAI", BaseURL: openaiURL, Protocol: llm.ProtocolResponses, KeyEnv: "OPENAI_API_KEY"},
	{ID: "anthropic", Label: "Anthropic", BaseURL: anthropicURL, Protocol: llm.ProtocolAnthropic, KeyEnv: "ANTHROPIC_API_KEY"},
	{ID: "meta", Label: "Meta", BaseURL: metaURL, Protocol: llm.ProtocolResponses, KeyEnv: "META_API_KEY"},
	{ID: "deepinfra", Label: "DeepInfra", BaseURL: deepinfraURL, Protocol: llm.ProtocolChat, KeyEnv: "DEEPINFRA_API_KEY"},
	{ID: "fireworks", Label: "Fireworks", BaseURL: fireworksURL, Protocol: llm.ProtocolChat, KeyEnv: "FIREWORKS_API_KEY"},
	{ID: "opencode", Label: "OpenCode Zen", BaseURL: zenURL, Protocol: llm.ProtocolChat, KeyEnv: "OPENCODE_ZEN_API_KEY", Note: "sells at provider cost; Claude models use the Anthropic protocol and GPT models the Responses protocol at the same address"},
	{ID: "nous", Label: "Nous Portal", BaseURL: nousURL, Protocol: llm.ProtocolChat, KeyEnv: "NOUS_API_KEY"},
}

const metaURL = "https://api.meta.ai/v1"

const asOf = "2026-09-07"

// asOfNew dates the entries added after the original pass; they were read
// from OpenRouter's live /api/v1/models on 2026-11-01.
const asOfNew = "2026-11-01"

func p(in, cached, out float64, source string) *Price {
	return &Price{In: in, Cached: cached, Out: out, AsOf: asOf, Source: source}
}
func pNew(in, cached, out float64, source string) *Price {
	return &Price{In: in, Cached: cached, Out: out, AsOf: asOfNew, Source: source}
}
func nominal(in, cached, out float64, source string) *Price {
	pr := p(in, cached, out, source)
	pr.Nominal = true
	return pr
}

var (
	glmEfforts      = []string{"low", "medium", "high"}
	kimiEfforts     = []string{"low", "medium", "high"}
	astraEfforts    = []string{"low", "medium", "high", "xhigh", "max"}
	gpt6Efforts     = []string{"none", "low", "medium", "high", "xhigh", "max"}
	gpt61SolEfforts = []string{"low", "medium", "high", "xhigh", "max"} // reasoning mandatory: no "none"
	claudeEfforts   = []string{"none", "low", "medium", "high", "xhigh", "max"}
	claude55Efforts = []string{"low", "medium", "high", "xhigh", "max"} // reasoning mandatory: no "none"
	museEfforts     = []string{"minimal", "low", "medium", "high", "xhigh", "max"}
	qwenEfforts     = []string{"low", "medium", "xhigh"}
	mistralEfforts  = []string{"low", "medium", "high"}
	museOpenEfforts = []string{"low", "medium", "high", "xhigh"}
	beamEfforts     = []string{"low", "medium", "high", "xhigh", "max"}
	v41FlashEfforts = []string{"none", "low", "high", "max"}
)

// Models, grouped by provider in the same order as Providers.
var Models = []Model{
	// Together AI
	{Provider: "together", ID: "zai-org/GLM-5.3", Family: "glm-5.3", Label: "GLM-5.3", Price: p(1.40, 0.26, 4.40, "Together price list"), Context: 200_000, Efforts: glmEfforts, Note: "latency varies a lot at any effort"},
	{Provider: "together", ID: "zai-org/GLM-5.3-Flash", Family: "glm-5.3-flash", Label: "GLM-5.3-Flash", Price: p(0.15, 0.03, 0.50, "Together price list"), Context: 200_000, Vision: true, Efforts: glmEfforts, Note: "low keeps it fast; the default effort thinks for a minute on simple steps"},
	{Provider: "together", ID: "deepseek-ai/DeepSeek-V4.1-Flash-1017", Family: "deepseek-v4.1-flash", Label: "DeepSeek V4.1 Flash 1017", Price: pNew(0.30, 0.006, 1.20, "Together price list"), Context: 1_048_576, Vision: true, Efforts: v41FlashEfforts, Note: "the Oct 17, 2026 snapshot, which retired V4-Flash-0731; off-peak hours halve the rate to $0.15/$0.60; no medium"},
	// OpenRouter
	{Provider: "openrouter", ID: "moonshotai/kimi-k3", Family: "kimi-k3", Label: "Kimi K3", Price: p(3.00, 0.30, 15.00, "OpenRouter"), Context: 1_000_000, Vision: true, Efforts: kimiEfforts},
	{Provider: "openrouter", ID: "z-ai/glm-5.3", Family: "glm-5.3", Label: "GLM-5.3", Price: p(1.40, 0.26, 4.40, "OpenRouter"), Context: 200_000, Efforts: glmEfforts},
	{Provider: "openrouter", ID: "z-ai/glm-5.3-flash", Family: "glm-5.3-flash", Label: "GLM-5.3-Flash", Price: p(0.07, 0.01, 0.25, "OpenRouter"), Context: 200_000, Vision: true, Efforts: glmEfforts},
	{Provider: "openrouter", ID: "deepseek/deepseek-v4-pro-0813", Family: "deepseek-v4-pro", Label: "DeepSeek V4 Pro 0813", Price: pNew(0.115, 0.11, 4.00, "OpenRouter /api/v1/models"), Context: 1_048_576, Efforts: v41FlashEfforts, Note: "the Aug 13, 2026 snapshot, which retired v4-pro; text only"},
	{Provider: "openrouter", ID: "deepseek/deepseek-v4.1-flash-1017", Family: "deepseek-v4.1-flash", Label: "DeepSeek V4.1 Flash 1017", Price: pNew(0.15, 0.003, 0.60, "OpenRouter /api/v1/models"), Context: 1_048_576, Vision: true, Efforts: v41FlashEfforts, Note: "the Oct 17, 2026 snapshot; weekday peak hours (UTC 0:00-16:00) double the rate to $0.30 in / $1.20 out / $0.006 cached; no medium, xhigh maps to max"},
	{Provider: "openrouter", ID: "mistralai/mistral-large-4-0", Family: "mistral-large-4", Label: "Mistral Large 4", Price: pNew(0.70, 0.07, 2.10, "OpenRouter /api/v1/models"), Context: 1_048_576, Vision: true, Efforts: mistralEfforts, Note: "1.05T total / 49B active MoE; OpenRouter's listing also offers 'none' but Mistral rejects it (live-checked 2026-10-31), and the default effort is high"},
	{Provider: "openrouter", ID: "meta/muse-spark-open", Family: "muse-spark-open", Label: "Muse Spark Open", Price: pNew(1.25, 0.15, 4.25, "OpenRouter /api/v1/models"), Context: 1_048_576, Vision: true, Efforts: museOpenEfforts, Note: "open weights released 2026-10-15; reasoning is always on and defaults to medium, not xhigh"},
	{Provider: "openrouter", ID: "reflection/beam-1.0-501b-a23b", Family: "beam-1.0", Label: "Beam 1.0 501B A23B", Price: pNew(0.25, 0.05, 1.00, "OpenRouter /api/v1/models"), Context: 1_048_576, Efforts: beamEfforts, Note: "501B total / 23B active MoE, text only; thinking is mandatory and defaults to max"},
	{Provider: "openrouter", ID: "qwen/qwen3.8-27b", Family: "qwen3.8-27b", Label: "Qwen 3.8 27B", Price: p(0.42, 0.42, 3.00, "OpenRouter"), Context: 200_000, Efforts: qwenEfforts, Note: "a dense model you could run locally"},
	{Provider: "openrouter", ID: "minimax/minimax-m3", Family: "minimax-m3", Label: "MiniMax M3", Price: p(0.18, 0.04, 0.77, "OpenRouter"), Context: 1_000_000, Vision: true},
	{Provider: "openrouter", ID: "openai/gpt-6-astra", Family: "gpt-6-astra", Label: "GPT-6 Astra", Protocol: llm.ProtocolResponses, Price: p(10.0, 1.00, 50.0, "OpenRouter"), Context: 1_050_000, Vision: true, Efforts: astraEfforts},
	{Provider: "openrouter", ID: "openai/gpt-6.1-sol", Family: "gpt-6.1-sol", Label: "GPT-6.1 Sol", Protocol: llm.ProtocolResponses, Price: pNew(2.00, 0.10, 10.0, "OpenRouter /api/v1/models"), Context: 1_050_000, Vision: true, Efforts: gpt61SolEfforts, Note: "released 2026-10-26; supersedes gpt-5.6-sol; tool calling needs the Responses protocol; reasoning mandatory, default medium"},
	{Provider: "openrouter", ID: "openai/gpt-6.1-terra", Family: "gpt-6.1-terra", Label: "GPT-6.1 Terra", Protocol: llm.ProtocolResponses, Price: pNew(1.00, 0.05, 5.00, "OpenRouter /api/v1/models"), Context: 1_050_000, Vision: true, Efforts: gpt6Efforts, Note: "released 2026-10-26; supersedes gpt-5.6-terra; tool calling needs the Responses protocol"},
	{Provider: "openrouter", ID: "openai/gpt-6-luna", Family: "gpt-6-luna", Label: "GPT-6 Luna", Protocol: llm.ProtocolResponses, Price: pNew(0.10, 0.01, 0.50, "OpenRouter /api/v1/models"), Context: 1_050_000, Vision: true, Efforts: gpt6Efforts, Note: "supersedes gpt-5.6-luna; cheapest of the GPT-6 family"},
	{Provider: "openrouter", ID: "anthropic/claude-opus-5.5", Family: "claude-opus-5.5", Label: "Claude Opus 5.5", Price: pNew(4.00, 0.20, 20.0, "OpenRouter /api/v1/models"), Context: 1_000_000, Vision: true, Efforts: claude55Efforts, Note: "supersedes claude-opus-5; first Opus price cut after generations at $5/$25"},
	{Provider: "openrouter", ID: "anthropic/claude-sonnet-5.5", Family: "claude-sonnet-5.5", Label: "Claude Sonnet 5.5", Price: pNew(2.00, 0.10, 10.0, "OpenRouter /api/v1/models"), Context: 1_000_000, Vision: true, Efforts: claude55Efforts, Note: "supersedes claude-sonnet-5"},
	{Provider: "openrouter", ID: "anthropic/claude-haiku-5.5", Family: "claude-haiku-5.5", Label: "Claude Haiku 5.5", Price: pNew(0.10, 0.01, 0.50, "OpenRouter /api/v1/models"), Context: 1_000_000, Vision: true, Efforts: claudeEfforts, Note: "supersedes claude-haiku-4.5; above 100K input tokens the rate rises to $0.50/$2.50"},
	// OpenAI
	{Provider: "openai", ID: "gpt-6-astra", Family: "gpt-6-astra", Label: "GPT-6 Astra", Price: p(10.0, 1.00, 50.0, "OpenAI price list"), Context: 1_050_000, Vision: true, Efforts: astraEfforts, Note: "no 'none'; reasoning items are replayed by default"},
	{Provider: "openai", ID: "gpt-6.1-sol", Family: "gpt-6.1-sol", Label: "GPT-6.1 Sol", Price: pNew(2.00, 0.10, 10.0, "OpenAI price list"), Context: 1_050_000, Vision: true, Efforts: gpt61SolEfforts, Note: "released 2026-10-26; supersedes gpt-5.6-sol; reasoning mandatory"},
	{Provider: "openai", ID: "gpt-6.1-terra", Family: "gpt-6.1-terra", Label: "GPT-6.1 Terra", Price: pNew(1.00, 0.05, 5.00, "OpenAI price list"), Context: 1_050_000, Vision: true, Efforts: gpt6Efforts, Note: "released 2026-10-26; supersedes gpt-5.6-terra"},
	{Provider: "openai", ID: "gpt-6-luna", Family: "gpt-6-luna", Label: "GPT-6 Luna", Price: pNew(0.10, 0.01, 0.50, "OpenAI price list"), Context: 1_050_000, Vision: true, Efforts: gpt6Efforts, Note: "supersedes gpt-5.6-luna; cheapest and fastest of the family"},
	// Anthropic
	{Provider: "anthropic", ID: "claude-fable-5-1", Family: "claude-fable-5.1", Label: "Claude Fable 5.1", Price: pNew(10.0, 0.25, 50.0, "Anthropic price list"), Context: 1_000_000, Vision: true, Efforts: claude55Efforts},
	{Provider: "anthropic", ID: "claude-opus-5-5", Family: "claude-opus-5.5", Label: "Claude Opus 5.5", Price: pNew(4.00, 0.20, 20.0, "Anthropic price list"), Context: 1_000_000, Vision: true, Efforts: claude55Efforts, Note: "supersedes claude-opus-5; reasoning mandatory"},
	{Provider: "anthropic", ID: "claude-sonnet-5-5", Family: "claude-sonnet-5.5", Label: "Claude Sonnet 5.5", Price: pNew(2.00, 0.10, 10.0, "Anthropic price list"), Context: 1_000_000, Vision: true, Efforts: claude55Efforts, Note: "supersedes claude-sonnet-5; reasoning mandatory"},
	{Provider: "anthropic", ID: "claude-haiku-5-5", Family: "claude-haiku-5.5", Label: "Claude Haiku 5.5", Price: pNew(0.10, 0.01, 0.50, "Anthropic price list"), Context: 1_000_000, Vision: true, Efforts: claudeEfforts, Note: "supersedes claude-haiku-4.5; above 100K input tokens the rate rises to $0.50/$2.50"},
	// Meta
	{Provider: "meta", ID: "muse-spark-1.3", Family: "muse-spark-1.3", Label: "Muse Spark 1.3", Price: p(1.25, 0.15, 4.25, "Meta price list"), Context: 1_000_000, Vision: true, Efforts: museEfforts, Note: "no 'none'; checked live 2026-09-07"},
	{Provider: "meta", ID: "muse-spark-1.3-contributor", Family: "muse-spark-1.3", Label: "Muse Spark 1.3 Contributor", Price: p(0.10, 0.002, 0.20, "Meta price list"), Context: 1_000_000, Vision: true, Efforts: museEfforts, Note: "the same model at up to 95% less: Meta trains on your inputs and outputs, rate limits by tokens, select countries only; no 'none'"},
	// DeepInfra
	{Provider: "deepinfra", ID: "moonshotai/Kimi-K3", Family: "kimi-k3", Label: "Kimi K3", Price: p(2.85, 0.285, 14.25, "DeepInfra catalog"), Context: 1_000_000, Vision: true, Efforts: kimiEfforts},
	{Provider: "deepinfra", ID: "zai-org/GLM-5.3", Family: "glm-5.3", Label: "GLM-5.3", Price: p(1.20, 0.12, 4.00, "DeepInfra catalog"), Context: 200_000, Efforts: glmEfforts, Note: "measured 35 to 44 s on a trivial call"},
	{Provider: "deepinfra", ID: "zai-org/GLM-5.3-Flash", Family: "glm-5.3-flash", Label: "GLM-5.3-Flash", Price: p(0.15, 0.03, 0.50, "DeepInfra catalog"), Context: 200_000, Vision: true, Efforts: glmEfforts},
	{Provider: "deepinfra", ID: "deepseek-ai/DeepSeek-V4.1-Flash-1017", Family: "deepseek-v4.1-flash", Label: "DeepSeek V4.1 Flash 1017", Price: pNew(0.098, 0.003, 0.294, "DeepInfra catalog"), Context: 1_048_576, Vision: true, Efforts: v41FlashEfforts, Note: "supersedes DeepSeek-V4-Flash-0731; standard-tier promo pricing, no medium"},
	{Provider: "deepinfra", ID: "deepseek-ai/DeepSeek-V4-Pro-0813", Family: "deepseek-v4-pro", Label: "DeepSeek V4 Pro", Price: p(1.30, 0.10, 2.60, "DeepInfra catalog"), Context: 1_000_000},
	{Provider: "deepinfra", ID: "Qwen/Qwen3.8-27B", Family: "qwen3.8-27b", Label: "Qwen 3.8 27B", Price: p(0.40, 0.04, 3.00, "DeepInfra catalog"), Context: 200_000, Efforts: qwenEfforts},
	// Fireworks
	{Provider: "fireworks", ID: "accounts/fireworks/models/kimi-k3", Family: "kimi-k3", Label: "Kimi K3", Price: p(3.00, 0.30, 15.00, "Fireworks model page"), Context: 1_000_000, Vision: true, Efforts: kimiEfforts},
	{Provider: "fireworks", ID: "accounts/fireworks/models/glm-5p3", Family: "glm-5.3", Label: "GLM-5.3", Price: p(1.40, 0.26, 4.40, "Fireworks model page"), Context: 200_000, Efforts: glmEfforts},
	{Provider: "fireworks", ID: "accounts/fireworks/models/glm-5p3-flash", Family: "glm-5.3-flash", Label: "GLM-5.3-Flash", Price: p(0.15, 0.03, 0.50, "Fireworks model page"), Context: 200_000, Vision: true, Efforts: glmEfforts},
	{Provider: "fireworks", ID: "accounts/fireworks/models/deepseek-v4p1-flash-1017", Family: "deepseek-v4.1-flash", Label: "DeepSeek V4.1 Flash 1017", Price: pNew(0.30, 0.009, 1.20, "Fireworks model page"), Context: 1_048_576, Vision: true, Efforts: v41FlashEfforts, Note: "serverless; supersedes deepseek-v4-flash-0731; no medium"},
	{Provider: "fireworks", ID: "accounts/fireworks/models/minimax-m3", Family: "minimax-m3", Label: "MiniMax M3", Context: 512_000},
	// OpenCode Zen
	{Provider: "opencode", ID: "glm-5.3", Family: "glm-5.3", Label: "GLM-5.3", Price: p(1.40, 0.26, 4.40, "OpenCode Zen price table"), Context: 200_000, Efforts: glmEfforts},
	{Provider: "opencode", ID: "glm-5.3-flash", Family: "glm-5.3-flash", Label: "GLM-5.3-Flash", Price: p(0.15, 0.03, 0.50, "OpenCode Zen price table"), Context: 200_000, Vision: true, Efforts: glmEfforts},
	{Provider: "opencode", ID: "kimi-k3", Family: "kimi-k3", Label: "Kimi K3", Price: p(3.00, 0.30, 15.00, "OpenCode Zen price table"), Context: 1_000_000, Vision: true, Efforts: kimiEfforts},
	{Provider: "opencode", ID: "deepseek-v4.1-flash-1017", Family: "deepseek-v4.1-flash", Label: "DeepSeek V4.1 Flash 1017", Price: pNew(0.30, 0.006, 1.20, "OpenCode Zen price table"), Context: 1_048_576, Vision: true, Efforts: v41FlashEfforts, Note: "supersedes deepseek-v4-flash; no medium"},
	{Provider: "opencode", ID: "minimax-m3", Family: "minimax-m3", Label: "MiniMax M3", Price: p(0.30, 0.06, 1.20, "OpenCode Zen price table"), Context: 1_000_000, Vision: true},
	{Provider: "opencode", ID: "claude-fable-5-1", Family: "claude-fable-5.1", Label: "Claude Fable 5.1", Protocol: llm.ProtocolAnthropic, Price: p(10.0, 0.25, 50.0, "OpenCode Zen price table"), Context: 1_000_000, Vision: true, Efforts: claude55Efforts},
	{Provider: "opencode", ID: "claude-opus-5-5", Family: "claude-opus-5.5", Label: "Claude Opus 5.5", Protocol: llm.ProtocolAnthropic, Price: pNew(4.00, 0.20, 20.0, "OpenCode Zen price table"), Context: 1_000_000, Vision: true, Efforts: claude55Efforts},
	{Provider: "opencode", ID: "claude-sonnet-5-5", Family: "claude-sonnet-5.5", Label: "Claude Sonnet 5.5", Protocol: llm.ProtocolAnthropic, Price: pNew(2.00, 0.10, 10.0, "OpenCode Zen price table"), Context: 1_000_000, Vision: true, Efforts: claude55Efforts},
	{Provider: "opencode", ID: "claude-haiku-5-5", Family: "claude-haiku-5.5", Label: "Claude Haiku 5.5", Protocol: llm.ProtocolAnthropic, Price: pNew(0.10, 0.01, 0.50, "OpenCode Zen price table"), Context: 1_000_000, Vision: true, Efforts: claudeEfforts},
	{Provider: "opencode", ID: "gpt-6-astra", Family: "gpt-6-astra", Label: "GPT-6 Astra", Protocol: llm.ProtocolResponses, Price: p(10.0, 1.00, 50.0, "OpenCode Zen price table"), Context: 1_050_000, Vision: true, Efforts: astraEfforts},
	{Provider: "opencode", ID: "gpt-6.1-sol", Family: "gpt-6.1-sol", Label: "GPT-6.1 Sol", Protocol: llm.ProtocolResponses, Price: pNew(2.00, 0.10, 10.0, "OpenCode Zen price table"), Context: 1_050_000, Vision: true, Efforts: gpt61SolEfforts},
	{Provider: "opencode", ID: "gpt-6.1-terra", Family: "gpt-6.1-terra", Label: "GPT-6.1 Terra", Protocol: llm.ProtocolResponses, Price: pNew(1.00, 0.05, 5.00, "OpenCode Zen price table"), Context: 1_050_000, Vision: true, Efforts: gpt6Efforts},
	{Provider: "opencode", ID: "gpt-6-luna", Family: "gpt-6-luna", Label: "GPT-6 Luna", Protocol: llm.ProtocolResponses, Price: pNew(0.10, 0.01, 0.50, "OpenCode Zen price table"), Context: 1_050_000, Vision: true, Efforts: gpt6Efforts},
	{Provider: "opencode", ID: "muse-spark-1.3", Family: "muse-spark-1.3", Label: "Muse Spark 1.3", Protocol: llm.ProtocolResponses, Price: p(1.25, 0.15, 4.25, "OpenCode Zen price table"), Context: 1_000_000, Vision: true, Efforts: museEfforts},
	// Nous Portal
	{Provider: "nous", ID: "moonshotai/kimi-k3", Family: "kimi-k3", Label: "Kimi K3", Price: p(2.04, 0.20, 10.20, "Nous Portal catalog"), Context: 1_000_000, Vision: true, Efforts: kimiEfforts},
	{Provider: "nous", ID: "z-ai/glm-5.3", Family: "glm-5.3", Label: "GLM-5.3", Price: p(0.94, 0.19, 3.17, "Nous Portal catalog"), Context: 200_000, Efforts: glmEfforts},
	{Provider: "nous", ID: "z-ai/glm-5.3-flash", Family: "glm-5.3-flash", Label: "GLM-5.3-Flash", Price: p(0.06, 0.01, 0.19, "Nous Portal catalog"), Context: 200_000, Vision: true, Efforts: glmEfforts},
	{Provider: "nous", ID: "deepseek/deepseek-v4.1-flash-1017", Family: "deepseek-v4.1-flash", Label: "DeepSeek V4.1 Flash 1017", Price: pNew(0.15, 0.003, 0.60, "Nous Portal catalog"), Context: 1_048_576, Vision: true, Efforts: v41FlashEfforts, Note: "supersedes deepseek-v4-flash-0731; no medium"},
}

// ProviderByID returns a provider and whether it exists.
func ProviderByID(id string) (Provider, bool) {
	for _, pr := range Providers {
		if pr.ID == id {
			return pr, true
		}
	}
	return Provider{}, false
}

// ProviderForURL finds the provider serving a base URL.
func ProviderForURL(baseURL string) (Provider, bool) {
	u := strings.TrimRight(baseURL, "/")
	for _, pr := range Providers {
		if strings.TrimRight(pr.BaseURL, "/") == u {
			return pr, true
		}
	}
	return Provider{}, false
}

// Lookup finds the catalog entry for a model at a base URL.
func Lookup(baseURL, model string) (Model, bool) {
	pr, ok := ProviderForURL(baseURL)
	if !ok {
		return Model{}, false
	}
	for _, m := range Models {
		if m.Provider == pr.ID && m.ID == model {
			return m, true
		}
	}
	return Model{}, false
}

// ProtocolFor is the wire protocol for a model at a provider: the model's
// override when it has one, else the provider's default.
func (m Model) ProtocolFor() string {
	if m.Protocol != "" {
		return m.Protocol
	}
	if pr, ok := ProviderByID(m.Provider); ok {
		return pr.Protocol
	}
	return llm.ProtocolChat
}

// Transport says how an effort value reaches the model for a protocol.
func Transport(protocol string) string {
	switch protocol {
	case llm.ProtocolResponses:
		return "sent as reasoning.effort (Responses API); 'none' is not sent"
	case llm.ProtocolAnthropic:
		return "adaptive thinking with output_config.effort; 'none' sends no thinking block"
	default:
		return "sent verbatim as reasoning_effort (chat completions)"
	}
}

// PriceFor returns the price of a model at a base URL, falling back to any
// provider's price for the same model id (a custom proxy in front of a known
// model), and whether one was found.
func PriceFor(baseURL, model string) (Price, bool) {
	if m, ok := Lookup(baseURL, model); ok && m.Price != nil {
		return *m.Price, true
	}
	// Behind an unknown host (a proxy, a self-hosted box) the id alone must
	// decide. That is safe only when every provider charges the same for it;
	// otherwise the figure would be a guess wearing a dollar sign.
	var found *Price
	for _, m := range Models {
		if m.ID != model || m.Price == nil {
			continue
		}
		if found == nil {
			found = m.Price
		} else if m.Price.In != found.In || m.Price.Cached != found.Cached || m.Price.Out != found.Out {
			return Price{}, false
		}
	}
	if found != nil {
		return *found, true
	}
	return Price{}, false
}

// KnownBaseURL reports whether a base URL belongs to a catalog provider.
func KnownBaseURL(baseURL string) bool { _, ok := ProviderForURL(baseURL); return ok }

// KnownKeyEnv reports whether an environment variable name is one a catalog
// provider uses for its key.
func KnownKeyEnv(name string) bool {
	for _, pr := range Providers {
		if pr.KeyEnv == name {
			return true
		}
	}
	return false
}

// KeyEnvs lists every key variable the catalog knows, sorted.
func KeyEnvs() []string {
	seen := map[string]bool{}
	var out []string
	for _, pr := range Providers {
		if !seen[pr.KeyEnv] {
			seen[pr.KeyEnv] = true
			out = append(out, pr.KeyEnv)
		}
	}
	sort.Strings(out)
	return out
}

// EffortIndex orders an effort value on the canonical scale; unknown values
// sort last.
func EffortIndex(e string) int {
	for i, v := range EffortOrder {
		if v == e {
			return i
		}
	}
	return len(EffortOrder)
}
