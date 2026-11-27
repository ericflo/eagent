package config

import (
	"context"
	"os"
	"testing"
)

// localGLM registers a keyless server at http://127.0.0.1:9/v1 whose list
// says it serves zai-org/GLM-5.3, the defaults' model, with a 32k window.
func localGLM(t *testing.T, project string) {
	t.Helper()
	if _, err := RegisterServer(context.Background(), project, ProbeOptions{URL: "http://127.0.0.1:9/v1", ID: "local", Label: "Local"}, true); err != nil {
		t.Fatal(err)
	}
	UpdateLiveCacheForServer(project, "local", []Model{{Provider: "local", ID: "zai-org/GLM-5.3", Context: 32_768}}, "")
}

// A route's window is its model's where it is reached: moving the defaults'
// GLM-5.3 to a registered server that serves it with 32k, by the file, a
// bundle over it, or EAGENT_BASE_URL, takes that server's window, not
// Together AI's 200k. The same address written with a trailing slash is not
// a move, and a window the layer names stays.
func TestTheWindowFollowsTheAddress(t *testing.T) {
	const togetherFallback = `"fallback":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}`
	for _, c := range []struct {
		name, file, bundle string
		env                map[string]string
		want, fallback     int
	}{
		{"file", `{"model":{"base_url":"http://127.0.0.1:9/v1","api_key_env":""}}`, "", nil, 32_768, 0},
		{"file with the editor's null window", `{"model":{"base_url":"http://127.0.0.1:9/v1","api_key_env":"","context_tokens":null}}`, "", nil, 32_768, 0},
		{"file names the window", `{"model":{"base_url":"http://127.0.0.1:9/v1","api_key_env":"","context_tokens":16000}}`, "", nil, 16_000, 0},
		{"bundle moves a fallback", `{"default_config":"local","model":{` + togetherFallback + `}}`, `{"model":{"fallback":{"base_url":"http://127.0.0.1:9/v1","api_key_env":""}}}`, nil, 200_000, 32_768},
		{"env", `{}`, "", map[string]string{"EAGENT_BASE_URL": "http://127.0.0.1:9/v1", "EAGENT_API_KEY_ENV": ""}, 32_768, 0},
		{"same address with a slash", `{"model":{"base_url":"https://api.together.xyz/v1/"}}`, "", nil, 200_000, 0},
		{"bundle, same fallback address with a slash", `{"default_config":"local","model":{` + togetherFallback + `}}`, `{"model":{"fallback":{"base_url":"https://api.together.xyz/v1/"}}}`, nil, 200_000, 200_000},
		{"env, same address with a slash", `{}`, "", map[string]string{"EAGENT_BASE_URL": "https://api.together.xyz/v1/"}, 200_000, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			unsetEnv(t, routeEnvNames()...)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			project := t.TempDir()
			localGLM(t, project)
			writeFile(t, project, c.file)
			if c.bundle != "" {
				if err := os.MkdirAll(BundlesDir(project), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(BundlePath(project, "local"), []byte(c.bundle), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Model.ContextTokens != c.want {
				t.Fatalf("context_tokens = %d, want %d (rollover %d)", cfg.Model.ContextTokens, c.want, cfg.Rollover())
			}
			if want := AutoRolloverWithin(c.want, cfg.Model.MaxTokens, 0); cfg.Rollover() != want {
				t.Fatalf("rollover = %d, want %d", cfg.Rollover(), want)
			}
			if c.fallback != 0 && (cfg.Model.Fallback == nil || cfg.Model.Fallback.ContextTokens != c.fallback) {
				t.Fatalf("fallback = %+v, want window %d", cfg.Model.Fallback, c.fallback)
			}
			// Resolve is given the bundle; it does not read default_config.
			bundle := ""
			if c.bundle != "" {
				bundle = "local"
			}
			res := Resolve(project, "", bundle)
			if got := res.Effective.Model.ContextTokens; got != c.want {
				t.Fatalf("Resolve: context_tokens = %d, want %d", got, c.want)
			}
			if fb := res.Effective.Model.Fallback; c.fallback != 0 && (fb == nil || fb.ContextTokens != c.fallback) {
				t.Fatalf("Resolve: fallback = %+v, want window %d", fb, c.fallback)
			}
		})
	}
}

// A window EAGENT_BASE_URL's server gives, where no layer names one, is the
// environment's doing ("env"), but no variable's: unlike the window of the
// model EAGENT_MODEL names, one the file names holds over it, so the
// editors do not show it as pinned (env:NAME) and it stays editable. The
// same address with a trailing slash changes no window and is not marked.
func TestTheWindowAnAddressVariableMovesIsTheVariables(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"http://127.0.0.1:9/v1", "env"},
		{"https://api.together.xyz/v1/", "default"},
	} {
		unsetEnv(t, routeEnvNames()...)
		t.Setenv("EAGENT_BASE_URL", c.url)
		t.Setenv("EAGENT_API_KEY_ENV", "")
		project := t.TempDir()
		localGLM(t, project)
		if got := Resolve(project, "", "").Sources["/model/context_tokens"]; got != c.want {
			t.Fatalf("%s: window source = %q, want %q", c.url, got, c.want)
		}
	}
}

// EAGENT_SUMMARIZER_BASE_URL moves the summarizer's copy of the main route
// to the server: its window is the server's too.
func TestTheSummarizersWindowFollowsTheAddress(t *testing.T) {
	unsetEnv(t, routeEnvNames()...)
	t.Setenv("EAGENT_SUMMARIZER_MODE", "dedicated")
	t.Setenv("EAGENT_SUMMARIZER_BASE_URL", "http://127.0.0.1:9/v1")
	t.Setenv("EAGENT_SUMMARIZER_API_KEY_ENV", "")
	project := t.TempDir()
	localGLM(t, project)
	cfg, err := Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if s := cfg.Summarizer.Model; s == nil || s.ContextTokens != 32_768 || cfg.Model.ContextTokens != 200_000 {
		t.Fatalf("summarizer %+v main %d", s, cfg.Model.ContextTokens)
	}
}
