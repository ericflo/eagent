package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
	"github.com/ericflo/eagent/internal/relay"
)

// The project's default_config lays a saved preset that sets only the
// route's key variable (no model of its own). `servers use deepinfra GLM`
// writes the file; loading then runs the written model at the written
// address with the saved preset's key variable. The note says what runs in
// full and that the key variable is what differs. (It said the saved preset
// "names its own model, so eagent still runs" the model just written.)
func TestK20ServersUseNoteNamesWhatDiffers(t *testing.T) {
	relay.OfflineCatalogs()
	project := sr12Clean(t)
	t.Setenv("DEEPINFRA_API_KEY", "x")
	t.Setenv("OTHER_KEY", "x")
	file := `{"default_config":"keys","finalechat":{"enabled":false}}`
	if err := os.WriteFile(config.File(project), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BundlePath(project, "keys"), []byte(`{"model":{"api_key_env":"OTHER_KEY"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(project, ""); err != nil {
		t.Fatalf("loads before: %v", err)
	}
	var code int
	var out string
	said := k18Stderr(t, func() {
		out = captureStdout(t, func() {
			code = cmdServersUse(context.Background(), project, []string{"deepinfra", "zai-org/GLM-5.3"}, serverCLIOpts{})
		})
	})
	now, err := config.Load(project, "")
	t.Logf("exit %d out %q err %q; runs %s @ %s key $%s (%v)", code, out, said, now.Model.Model, now.Model.BaseURL, now.Model.APIKeyEnv, err)
	if err != nil || code != 0 || now.Model.Model != "zai-org/GLM-5.3" || now.Model.APIKeyEnv != "OTHER_KEY" {
		t.Fatalf("exit %d (%v); runs %s with $%s", code, err, now.Model.Model, now.Model.APIKeyEnv)
	}
	for _, want := range []string{
		`the saved preset "keys" is laid over the project file here`,
		"eagent runs zai-org/GLM-5.3 @ https://api.deepinfra.com/v1/openai (openai-chat, key $OTHER_KEY)",
		"what was written (zai-org/GLM-5.3 @ https://api.deepinfra.com/v1/openai (openai-chat, key $DEEPINFRA_API_KEY)) in its key variable.",
		"the project's default_config lays it here",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the note lacks %q: %q", want, out)
		}
	}
	if strings.Contains(out, "eagent -> ") || strings.Contains(out, "model, ") || strings.Contains(out, "names its own model") {
		t.Errorf("the note blames the model, or prints the route as if it ran as written: %q", out)
	}
}
