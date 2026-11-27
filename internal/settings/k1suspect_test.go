package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// The reader's first suspicion of d7f7b75, not a fault: a route the editor
// moves with its window at 0 (none shown) is saved with the window null,
// and loads with the window of the model where it is now reached. That is
// the owner's rule for a null window over another model (round 10 finding
// 18), and followModel has applied it to another address since a9e5c5a
// (kept in b575552); 151cc19 only writes a window the form shows, and a 0
// shows none. Both moves load alike.
func TestK1AMovedRouteWithNoWindowLoadsTheKnownWindow(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*config.ModelRoute)
		want int
	}{
		{"another model", func(a *config.ModelRoute) { a.Model = "deepseek-ai/DeepSeek-V4.1-Flash-1017" }, 1_048_576},
		{"another address", func(a *config.ModelRoute) { a.BaseURL, a.APIKeyEnv = "http://127.0.0.1:9/v1", "" }, 32_768},
	} {
		t.Run(c.name, func(t *testing.T) {
			clearRouteEnv(t)
			project := t.TempDir()
			k1LocalGLM(t, project)
			before := config.Resolve(project, "", "")
			cfg := before.Effective
			c.edit(&cfg.Model)
			cfg.Model.ContextTokens = 0
			raw, _ := json.Marshal(cfg)
			loaded, _ := json.Marshal(before.Effective)
			res, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw, Loaded: loaded})
			if err != nil {
				t.Fatal(err)
			}
			if got := res.Resolution.Effective.Model.ContextTokens; got != c.want {
				t.Fatalf("window after save = %d, want %d", got, c.want)
			}
		})
	}
}
