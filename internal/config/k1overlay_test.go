package config

import (
	"encoding/json"
	"testing"
)

// Finding 10 of the fifteenth review: a route the editor moves to another
// address for the same model, with a window equal to the base's, keeps
// that window. The loader refreshes a route's window when its model or its
// address changes (moved, followModel), so Overlay writes the window shown
// for such a route, as for a new model: on the main route and on a
// fallback. The same address with a trailing slash is no move.
func TestK1AWindowShownForAMovedAddressIsWritten(t *testing.T) {
	const local = "http://127.0.0.1:9/v1"
	for _, c := range []struct {
		name, preset string
		edit         func(*Config)
		check        func(Config) int
		want         int
	}{
		{"main route", "glm", func(c *Config) { c.Model.BaseURL, c.Model.APIKeyEnv = local, "" }, func(c Config) int { return c.Model.ContextTokens }, 200_000},
		{"fallback", "openai-high", func(c *Config) { c.Model.Fallback.BaseURL, c.Model.Fallback.APIKeyEnv = local, "" }, func(c Config) int {
			if c.Model.Fallback == nil {
				return -1
			}
			return c.Model.Fallback.ContextTokens
		}, 1_050_000},
		{"same address with a slash", "glm", func(c *Config) { c.Model.BaseURL = "https://api.together.xyz/v1/" }, func(c Config) int { return c.Model.ContextTokens }, 200_000},
	} {
		t.Run(c.name, func(t *testing.T) {
			unsetEnv(t, routeEnvNames()...)
			project := t.TempDir()
			localGLM(t, project)
			UpdateLiveCacheForServer(project, "local", []Model{{Provider: "local", ID: "zai-org/GLM-5.3", Context: 32_768}, {Provider: "local", ID: "openai/gpt-6-astra", Context: 32_768}}, "")
			cfg := Defaults()
			Presets[c.preset](&cfg)
			c.edit(&cfg)
			edits, err := Overlay(cfg, c.preset)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(edits)
			writeFile(t, project, string(raw))
			loaded, err := Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := c.check(loaded); got != c.want {
				t.Fatalf("window after a save and load = %d, want %d (file %s)", got, c.want, raw)
			}
		})
	}
}
