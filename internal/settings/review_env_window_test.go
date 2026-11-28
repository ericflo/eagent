package settings

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The window EAGENT_MODEL's model brings is the environment's, like the
// model: a save of anything else while the variable is set must not write
// it, or once the variable is gone the file's model runs at another
// model's window.
func TestSaveKeepsEnvModelWindowOutOfTheFile(t *testing.T) {
	clearRouteEnv(t)
	t.Setenv("EAGENT_MODEL", "deepseek-ai/DeepSeek-V4.1-Flash-1017")
	project := t.TempDir()
	res := config.Resolve(project, "", "")
	if res.LoadError != "" || res.Effective.Model.ContextTokens != 1_048_576 {
		t.Fatalf("resolve: %q %+v", res.LoadError, res.Effective.Model)
	}
	cfg := res.Effective
	cfg.TickSeconds = 45
	raw, _ := json.Marshal(cfg)
	if _, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw}); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(config.File(project))
	os.Unsetenv("EAGENT_MODEL")
	loaded, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Model.Model != "zai-org/GLM-5.3" || loaded.Model.ContextTokens != 200_000 {
		t.Fatalf("after the variable is gone: model %q window %d\n%s", loaded.Model.Model, loaded.Model.ContextTokens, written)
	}
}
