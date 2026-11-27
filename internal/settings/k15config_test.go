package settings

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/protocol/control"
	"github.com/ericflo/eagent/internal/relay"
)

// A preset chosen in the settings (the phone's or the page's) over a file
// that names where its model is reached, as `eagent servers use` writes
// it, runs the preset's whole route: its model at its own address, with
// its protocol and key variable. (The file's address stayed, so OpenAI's
// model ran at DeepInfra, or the file's model ran with the preset's
// protocol.) The route's effort and reply ceiling stay in the file, and
// so does its fallback. The undo loads as the file did before.
func TestK15APresetChosenInTheSettingsRunsItsWholeRoute(t *testing.T) {
	relay.OfflineCatalogs()
	for _, c := range []struct{ name, file string }{
		{"an address and a key variable", `{"model":{"base_url":"https://api.deepinfra.com/v1/openai","api_key_env":"DEEPINFRA_API_KEY"}}`},
		{"a model at an address", `{"model":{"model":"zai-org/GLM-5.2","base_url":"https://api.deepinfra.com/v1/openai","api_key_env":"DEEPINFRA_API_KEY"}}`},
		{"a protocol alone", `{"model":{"protocol":"openai-responses"}}`},
		{"with a reply ceiling and a fallback", `{"model":{"base_url":"https://api.deepinfra.com/v1/openai","api_key_env":"DEEPINFRA_API_KEY","max_tokens":4096,"fallback":{"model":"zai-org/GLM-5.3","protocol":"openai-chat","base_url":"https://api.together.xyz/v1","api_key_env":"TOGETHER_API_KEY"}}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			clearRouteEnv(t)
			t.Setenv("EAGENT_FINALECHAT", "")
			for _, key := range []string{"OPENAI_API_KEY", "DEEPINFRA_API_KEY", "TOGETHER_API_KEY"} {
				t.Setenv(key, "x")
			}
			project := t.TempDir()
			writeProjectFile(t, project, c.file)
			before, err := config.Load(project, "")
			if err != nil {
				t.Fatal(err)
			}
			edits := []control.Edit{{Op: "set", Key: "/preset", Value: "openai-high"}}
			undone := r11Undo(t, project, c.file, edits)
			got, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("load after the change: %v", err)
			}
			want := config.Defaults()
			config.Presets["openai-high"](&want)
			if got.Model.Model != want.Model.Model || got.Model.BaseURL != want.Model.BaseURL || got.Model.Protocol != want.Model.Protocol || got.Model.APIKeyEnv != want.Model.APIKeyEnv {
				written, _ := os.ReadFile(config.File(project))
				t.Fatalf("runs %s @ %s (%s, $%s); want the preset's %s @ %s (%s, $%s)\nfile: %s", got.Model.Model, got.Model.BaseURL, got.Model.Protocol, got.Model.APIKeyEnv, want.Model.Model, want.Model.BaseURL, want.Model.Protocol, want.Model.APIKeyEnv, written)
			}
			if before.Model.MaxTokens == 4096 && (got.Model.MaxTokens != 4096 || got.Model.Fallback == nil || got.Model.Fallback.Model != "zai-org/GLM-5.3") {
				t.Fatalf("the reply ceiling or the fallback went: %d, %+v", got.Model.MaxTokens, got.Model.Fallback)
			}
			var back, was map[string]any
			if json.Unmarshal([]byte(undone), &back) != nil || json.Unmarshal([]byte(c.file), &was) != nil {
				t.Fatalf("undo wrote %s", undone)
			}
			writeProjectFile(t, project, undone)
			again, err := config.Load(project, "")
			if err != nil {
				t.Fatalf("load after the undo: %v\n%s", err, undone)
			}
			if again.Preset != before.Preset || again.Model.Model != before.Model.Model || again.Model.BaseURL != before.Model.BaseURL || again.Model.Protocol != before.Model.Protocol || again.Model.APIKeyEnv != before.Model.APIKeyEnv {
				t.Fatalf("after the undo: %s @ %s (%s, $%s); before: %s @ %s (%s, $%s)\nundo wrote: %s", again.Model.Model, again.Model.BaseURL, again.Model.Protocol, again.Model.APIKeyEnv, before.Model.Model, before.Model.BaseURL, before.Model.Protocol, before.Model.APIKeyEnv, undone)
			}
		})
	}
}

// A preset chosen together with a model or an address is that route's
// pick, as before: the file keeps what the proposal names.
func TestK15APresetChosenWithAModelKeepsTheModel(t *testing.T) {
	relay.OfflineCatalogs()
	clearRouteEnv(t)
	t.Setenv("EAGENT_FINALECHAT", "")
	t.Setenv("OPENAI_API_KEY", "x")
	project := t.TempDir()
	writeProjectFile(t, project, `{"model":{"model":"zai-org/GLM-5.3-Flash"}}`)
	if err := tryApply(t, &Service{Project: project}, control.Edit{Op: "set", Key: "/preset", Value: "openai-high"}, control.Edit{Op: "set", Key: "/model/model", Value: "gpt-6.1-sol"}); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(project, "")
	if err != nil || got.Model.Model != "gpt-6.1-sol" || got.Model.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("runs %s @ %s (%v); want the model the proposal named, at the preset's address", got.Model.Model, got.Model.BaseURL, err)
	}
}
