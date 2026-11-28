package config

import (
	"strings"
	"testing"
)

// EAGENT_MODEL (or EAGENT_SUMMARIZER_MODEL) names the model the route
// already has, and the address variable moves the route to a server that
// serves it with another window. The model variable moved nothing: the
// window is the address's doing ("env"), editable, as it is without the
// model variable. It was shown as the model variable's, locked.
func TestAModelVariableNamingTheRoutesOwnModelDoesNotOwnItsWindow(t *testing.T) {
	const local = "http://127.0.0.1:9/v1"
	for _, c := range []struct {
		name, file, key string
		env             map[string]string
	}{
		{"main", `{}`, "/model/context_tokens",
			map[string]string{"EAGENT_MODEL": "zai-org/GLM-5.3", "EAGENT_BASE_URL": local, "EAGENT_API_KEY_ENV": ""}},
		{"summarizer", `{"summarizer":{"mode":"dedicated","model":{"protocol":"openai-chat","base_url":"https://api.together.xyz/v1","model":"zai-org/GLM-5.3","api_key_env":"TOGETHER_API_KEY"}}}`, "/summarizer/model/context_tokens",
			map[string]string{"EAGENT_SUMMARIZER_MODEL": "zai-org/GLM-5.3", "EAGENT_SUMMARIZER_BASE_URL": local, "EAGENT_SUMMARIZER_API_KEY_ENV": ""}},
	} {
		t.Run(c.name, func(t *testing.T) {
			unsetEnv(t, routeEnvNames()...)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			project := t.TempDir()
			localGLM(t, project)
			writeFile(t, project, c.file)
			res := Resolve(project, "", "")
			if res.LoadError != "" {
				t.Fatal(res.LoadError)
			}
			route := res.Effective.Model
			if c.name == "summarizer" {
				route = *res.Effective.Summarizer.Model
			}
			if route.ContextTokens != 32_768 {
				t.Fatalf("setup: the window is %d, want the local server's", route.ContextTokens)
			}
			if got := res.Sources[c.key]; got != "env" {
				t.Fatalf("the source of %s is %q, want env", c.key, got)
			}
			model := strings.TrimSuffix(c.key, "context_tokens") + "model"
			if got := res.Sources[model]; strings.HasPrefix(got, "env") {
				t.Fatalf("the source of %s is %q: the variable changed nothing", model, got)
			}
		})
	}
}

// EAGENT_MODEL (or EAGENT_SUMMARIZER_MODEL) names another model that has
// the same window as the one the layers give. The window follows the
// variable's model whatever the file says of it (applyEnv), so it is the
// variable's, locked. It was shown as the preset's, editable: a window
// saved for it was accepted and never took effect.
func TestTheWindowOfAModelAVariableNamesIsTheVariablesEvenWhenItIsTheSameNumber(t *testing.T) {
	for _, c := range []struct{ name, file, key, variable string }{
		{"main", `{"preset":"openai-high"}`, "/model/context_tokens", "EAGENT_MODEL"},
		{"summarizer", `{"preset":"openai-high","summarizer":{"mode":"dedicated","model":{"protocol":"openai-responses","base_url":"https://api.openai.com/v1","model":"gpt-6-astra","api_key_env":"OPENAI_API_KEY"}}}`, "/summarizer/model/context_tokens", "EAGENT_SUMMARIZER_MODEL"},
	} {
		t.Run(c.name, func(t *testing.T) {
			unsetEnv(t, routeEnvNames()...)
			project := t.TempDir()
			writeFile(t, project, c.file)
			before := Resolve(project, "", "")
			t.Setenv(c.variable, "gpt-6.1-sol")
			res := Resolve(project, "", "")
			if res.LoadError != "" {
				t.Fatal(res.LoadError)
			}
			window := func(r Resolution) (string, int) {
				if c.name == "summarizer" {
					return r.Effective.Summarizer.Model.Model, r.Effective.Summarizer.Model.ContextTokens
				}
				return r.Effective.Model.Model, r.Effective.Model.ContextTokens
			}
			was, wasWindow := window(before)
			now, nowWindow := window(res)
			if was == now || wasWindow != nowWindow || nowWindow == 0 {
				t.Fatalf("setup: %s (%d) then %s (%d): want another model with the same window", was, wasWindow, now, nowWindow)
			}
			if got := res.Sources[c.key]; got != "env:"+c.variable {
				t.Fatalf("the source of %s is %q, want env:%s", c.key, got, c.variable)
			}
		})
	}
}
