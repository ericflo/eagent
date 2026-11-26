package settings

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ericflo/eagent/internal/config"
)

// Finding 25: a JSON null in config.json is a deliberate "cleared"
// override. Saving an unrelated field while the environment pins those
// fields carries the nulls over unchanged and says so.
func TestSaveUnderEnvKeepsNullOverrides(t *testing.T) {
	clearRouteEnv(t)
	project := t.TempDir()
	writeFile(t, config.File(project), `{"preset":"glm","model":{"reasoning_effort":null,"max_tokens":null}}`)
	t.Setenv("EAGENT_REASONING_EFFORT", "low")
	t.Setenv("EAGENT_MAX_TOKENS", "1000")
	res := config.Resolve(project, "", "")
	if res.LoadError != "" {
		t.Fatal(res.LoadError)
	}
	cfg := res.Effective
	cfg.Persona = "terse"
	raw, _ := json.Marshal(cfg)
	out, err := (&Service{Project: project}).Save(context.Background(), Request{Config: raw, BasePreset: "glm"})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(config.File(project))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Model map[string]json.RawMessage `json:"model"`
	}
	if err := json.Unmarshal(disk, &file); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"reasoning_effort", "max_tokens"} {
		if v, ok := file.Model[key]; !ok || string(v) != "null" {
			t.Fatalf("model.%s after the save = %q (present %v); file:\n%s", key, v, ok, disk)
		}
	}
	kept := map[string]bool{}
	for _, f := range out.Kept {
		kept[f.Pointer] = true
	}
	if !kept["/model/reasoning_effort"] || !kept["/model/max_tokens"] {
		t.Fatalf("Kept = %+v", out.Kept)
	}
	if !strings.Contains(string(disk), `"terse"`) {
		t.Fatalf("persona not saved:\n%s", disk)
	}
}
