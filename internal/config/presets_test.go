package config

import "testing"

// Each preset runs the model its orchestrator ran in the three-actor design
// (git main before the one-loop change), on the same route: protocol, base
// URL, key variable, context window and fallback. The values are copied
// from main's Presets. Effort and rollover_tokens are not checked here.
func TestPresetsKeepTheirOrchestratorsRoute(t *testing.T) {
	for _, w := range []struct {
		preset, model, protocol, baseURL, keyEnv string
		contextTokens                            int
		fallbackModel, fallbackURL, fallbackKey  string
	}{
		{"glm", "zai-org/GLM-5.3", "openai-chat", "https://api.together.xyz/v1", "TOGETHER_API_KEY", 200000, "", "", ""},
		{"astra", "gpt-6-astra", "openai-responses", "https://api.openai.com/v1", "OPENAI_API_KEY", 1050000, "openai/gpt-6-astra", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"},
		{"openai-high", "gpt-6-astra", "openai-responses", "https://api.openai.com/v1", "OPENAI_API_KEY", 1050000, "openai/gpt-6-astra", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"},
		{"openai-med", "gpt-6-astra", "openai-responses", "https://api.openai.com/v1", "OPENAI_API_KEY", 1050000, "openai/gpt-6-astra", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"},
		{"openai-low", "gpt-6.1-sol", "openai-responses", "https://api.openai.com/v1", "OPENAI_API_KEY", 1050000, "openai/gpt-6.1-sol", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"},
		{"openrouter-high", "moonshotai/kimi-k3", "openai-chat", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", 1000000, "", "", ""},
		{"openrouter-med", "z-ai/glm-5.3", "openai-chat", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", 200000, "", "", ""},
		{"openrouter-low", "z-ai/glm-5.3-flash", "openai-chat", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", 200000, "", "", ""},
		{"anthropic-high", "claude-fable-5-1", "anthropic", "https://api.anthropic.com/v1", "ANTHROPIC_API_KEY", 1000000, "", "", ""},
		{"anthropic-med", "claude-opus-5-5", "anthropic", "https://api.anthropic.com/v1", "ANTHROPIC_API_KEY", 1000000, "", "", ""},
		{"deepinfra-high", "moonshotai/Kimi-K3", "openai-chat", "https://api.deepinfra.com/v1/openai", "DEEPINFRA_API_KEY", 1000000, "", "", ""},
		{"deepinfra-med", "zai-org/GLM-5.3", "openai-chat", "https://api.deepinfra.com/v1/openai", "DEEPINFRA_API_KEY", 200000, "", "", ""},
		{"deepinfra-low", "zai-org/GLM-5.3-Flash", "openai-chat", "https://api.deepinfra.com/v1/openai", "DEEPINFRA_API_KEY", 200000, "", "", ""},
		{"fireworks-high", "accounts/fireworks/models/kimi-k3", "openai-chat", "https://api.fireworks.ai/inference/v1", "FIREWORKS_API_KEY", 1000000, "", "", ""},
		{"fireworks-med", "accounts/fireworks/models/glm-5p3", "openai-chat", "https://api.fireworks.ai/inference/v1", "FIREWORKS_API_KEY", 200000, "", "", ""},
		{"fireworks-low", "accounts/fireworks/models/glm-5p3-flash", "openai-chat", "https://api.fireworks.ai/inference/v1", "FIREWORKS_API_KEY", 200000, "", "", ""},
		{"opencode-high", "claude-fable-5-1", "anthropic", "https://opencode.ai/zen/v1", "OPENCODE_ZEN_API_KEY", 1000000, "", "", ""},
		{"opencode-med", "glm-5.3", "openai-chat", "https://opencode.ai/zen/v1", "OPENCODE_ZEN_API_KEY", 200000, "", "", ""},
		{"opencode-low", "glm-5.3-flash", "openai-chat", "https://opencode.ai/zen/v1", "OPENCODE_ZEN_API_KEY", 200000, "", "", ""},
		{"nous-high", "moonshotai/kimi-k3", "openai-chat", "https://inference-api.nousresearch.com/v1", "NOUS_API_KEY", 1000000, "", "", ""},
		{"nous-med", "z-ai/glm-5.3", "openai-chat", "https://inference-api.nousresearch.com/v1", "NOUS_API_KEY", 200000, "", "", ""},
		{"nous-low", "z-ai/glm-5.3-flash", "openai-chat", "https://inference-api.nousresearch.com/v1", "NOUS_API_KEY", 200000, "", "", ""},
		{"deepseek", "deepseek-ai/DeepSeek-V4.1-Flash-1017", "openai-chat", "https://api.together.xyz/v1", "TOGETHER_API_KEY", 1048576, "", "", ""},
		{"qwen", "qwen/qwen3.8-27b", "openai-chat", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", 200000, "", "", ""},
		{"muse", "muse-spark-1.3-contributor", "openai-responses", "https://api.meta.ai/v1", "META_API_KEY", 1000000, "", "", ""},
		{"mistral", "mistralai/mistral-large-4-0", "openai-chat", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", 1048576, "", "", ""},
		{"muse-open", "meta/muse-spark-open", "openai-chat", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", 1048576, "", "", ""},
		{"beam", "reflection/beam-1.0-501b-a23b", "openai-chat", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", 1048576, "", "", ""},
		{"deepseek-1017", "deepseek/deepseek-v4.1-flash-1017", "openai-chat", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", 1048576, "", "", ""},
	} {
		c := Defaults()
		Presets[w.preset](&c)
		m := c.Model
		if m.Model != w.model || m.Protocol != w.protocol || m.BaseURL != w.baseURL || m.APIKeyEnv != w.keyEnv || m.ContextTokens != w.contextTokens || m.MaxTokens != 32768 {
			t.Errorf("%s: model = %s %s %s %s %d (max %d), want %s %s %s %s %d (max 32768)", w.preset, m.Model, m.Protocol, m.BaseURL, m.APIKeyEnv, m.ContextTokens, m.MaxTokens, w.model, w.protocol, w.baseURL, w.keyEnv, w.contextTokens)
		}
		switch {
		case w.fallbackModel == "" && m.Fallback != nil:
			t.Errorf("%s: unexpected fallback %+v", w.preset, *m.Fallback)
		case w.fallbackModel != "" && (m.Fallback == nil || m.Fallback.Model != w.fallbackModel || m.Fallback.BaseURL != w.fallbackURL || m.Fallback.APIKeyEnv != w.fallbackKey ||
			m.Fallback.Protocol != w.protocol || m.Fallback.ContextTokens != w.contextTokens || m.Fallback.Fallback != nil):
			t.Errorf("%s: fallback = %+v, want %s at %s (%s)", w.preset, m.Fallback, w.fallbackModel, w.fallbackURL, w.fallbackKey)
		}
	}
	if len(PresetNames()) != 29 {
		t.Fatalf("PresetNames has %d presets; main had 29 and the table lists them all", len(PresetNames()))
	}
}
