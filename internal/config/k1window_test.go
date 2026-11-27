package config

import (
	"os"
	"testing"
)

// An address variable that moves a route refreshes its window only when no
// layer names a window for the route; a window a layer names stays, and is
// shown as that layer's (editable).
func TestK1AnAddressVariableRefreshesOnlyAWindowNoLayerNames(t *testing.T) {
	for _, c := range []struct {
		name, file, bundle string
		env                map[string]string
		want, summarizer   int
		source             string
	}{
		// The file names the window for the defaults' model; the variable
		// gives the server's address: the file's window stays.
		{"the file names the window", `{"model":{"context_tokens":16000}}`, "", map[string]string{"EAGENT_BASE_URL": "http://127.0.0.1:9/v1", "EAGENT_API_KEY_ENV": ""}, 16_000, 0, "file"},
		// The editor's null names none: the server's window.
		{"the file's null window", `{"model":{"context_tokens":null}}`, "", map[string]string{"EAGENT_BASE_URL": "http://127.0.0.1:9/v1", "EAGENT_API_KEY_ENV": ""}, 32_768, 0, "env"},
		// A bundle that moves the route the file named a window for drops
		// that window (followModel); the variable then moves the bundle's
		// route, whose window no layer names.
		{"a bundle moves the route the file named a window for", `{"default_config":"local","model":{"model":"other-model","context_tokens":16000}}`, `{"model":{"model":"zai-org/GLM-5.3"}}`, map[string]string{"EAGENT_BASE_URL": "http://127.0.0.1:9/v1", "EAGENT_API_KEY_ENV": ""}, 32_768, 0, "env"},
		// A bundle names the window over the file: it stays.
		{"the bundle names the window", `{"default_config":"local"}`, `{"model":{"context_tokens":20000}}`, map[string]string{"EAGENT_BASE_URL": "http://127.0.0.1:9/v1", "EAGENT_API_KEY_ENV": ""}, 20_000, 0, "bundle:local"},
		// The summarizer's copy of a route whose window the file names,
		// moved by its own address variable, keeps that window.
		{"the summarizer's copy of a named window", `{"model":{"context_tokens":16000}}`, "", map[string]string{"EAGENT_SUMMARIZER_MODE": "dedicated", "EAGENT_SUMMARIZER_BASE_URL": "http://127.0.0.1:9/v1", "EAGENT_SUMMARIZER_API_KEY_ENV": ""}, 16_000, 16_000, "file"},
		// The summarizer's own route with a window the file names.
		{"the summarizer's own named window", `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY","context_tokens":12000}}}`, "", map[string]string{"EAGENT_SUMMARIZER_BASE_URL": "http://127.0.0.1:9/v1", "EAGENT_SUMMARIZER_API_KEY_ENV": ""}, 200_000, 12_000, "default"},
		// The summarizer's own route with no window: the server's.
		{"the summarizer's own route without one", `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}}`, "", map[string]string{"EAGENT_SUMMARIZER_BASE_URL": "http://127.0.0.1:9/v1", "EAGENT_SUMMARIZER_API_KEY_ENV": ""}, 200_000, 32_768, "default"},
	} {
		t.Run(c.name, func(t *testing.T) {
			unsetEnv(t, routeEnvNames()...)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			project := t.TempDir()
			localGLM(t, project)
			writeFile(t, project, c.file)
			bundle := ""
			if c.bundle != "" {
				bundle = "local"
				if err := os.MkdirAll(BundlesDir(project), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(BundlePath(project, bundle), []byte(c.bundle), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			res := Resolve(project, "", bundle)
			for what, got := range map[string]int{"Load": cfg.Model.ContextTokens, "Resolve": res.Effective.Model.ContextTokens} {
				if got != c.want {
					t.Fatalf("%s: context_tokens = %d, want %d", what, got, c.want)
				}
			}
			if c.summarizer != 0 {
				for what, s := range map[string]*ModelRoute{"Load": cfg.Summarizer.Model, "Resolve": res.Effective.Summarizer.Model} {
					if s == nil || s.ContextTokens != c.summarizer {
						t.Fatalf("%s: summarizer %+v, want window %d", what, s, c.summarizer)
					}
				}
			}
			if got := res.Sources["/model/context_tokens"]; got != c.source {
				t.Fatalf("window source = %q, want %q", got, c.source)
			}
		})
	}
}
