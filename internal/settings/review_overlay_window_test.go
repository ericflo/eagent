package settings

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// A window set by hand for a new model is kept even when it equals the
// window of the preset's model: the file must name it, or loading gives the
// new model its catalog window instead of the one saved.
func TestSaveKeepsHandSetWindowEqualToPresets(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	res := config.Resolve(project, "", "")
	cfg := res.Effective
	base := cfg.Model.ContextTokens
	cfg.Model.Model = "deepseek-ai/DeepSeek-V4.1-Flash-1017"
	cfg.Model.ContextTokens = base // capped by hand at the old model's window
	if known := config.KnownContext(project, cfg.Model.BaseURL, cfg.Model.Model); known == base || known == 0 {
		t.Fatalf("test needs a model whose window differs: %d vs %d", known, base)
	}
	raw, _ := json.Marshal(cfg)
	if _, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw}); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(config.File(project))
	loaded, err := config.Load(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Model.ContextTokens != base {
		t.Fatalf("saved window %d, loaded %d\n%s", base, loaded.Model.ContextTokens, written)
	}
}
